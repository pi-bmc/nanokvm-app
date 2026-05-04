package firmware

// mount.go provides loop-device based read/write access to the firmware
// image, optimised around two facts about the Linux mass-storage gadget:
//
//   1. f_mass_storage opens the LUN file with vfs_iter_read/vfs_iter_write
//      via the page cache. Concurrent local mounts are unsafe because the
//      gadget and the local vfat driver each cache state independently
//      (FAT chains, dirents) → split-brain on writes.
//
//   2. A loop device sitting attached to the same inode is *not* an
//      exclusive holder; it only opens an fd. No I/O happens until the
//      loop device is itself read/written (i.e. mounted). So the loop
//      device can be attached for the lifetime of the controller and
//      shared with the gadget at zero cost.
//
// We therefore:
//
//   • losetup -P once at Init, stash the resulting device on the controller.
//   • For every read/write window: unpresent gadget → mount partition →
//     fn() → sync → umount → drop_caches → present gadget. Loop stays put.
//   • On image replacement (download): detach loop, replace file, re-attach.
//
// All of these mutate controller state and must run with c.mu held. The
// public Controller methods take the lock.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// withMount runs fn with c.imagePath mounted read/write at c.mountPoint.
// The USB gadget is unpresented for the duration and re-presented after,
// even on error. The persistent loop device is reused; only the mount
// itself is created and torn down. Must be called with c.mu held.
func (c *Controller) withMount(fn func() error) error {
	if !c.imageExists() {
		return fmt.Errorf("firmware image not found: %s", c.imagePath)
	}
	if c.loopDev == "" {
		// Lazy attach: Init may have skipped this if the gadget configfs
		// wasn't available at startup, or the image was downloaded later.
		if err := c.attachLoopLocked(); err != nil {
			return fmt.Errorf("attach loop: %w", err)
		}
	}

	wasPresented := c.presented
	if wasPresented {
		if err := c.unpresentImage(); err != nil {
			return fmt.Errorf("unpresent gadget: %w", err)
		}
	}
	defer func() {
		if wasPresented {
			if err := c.presentImage(); err != nil {
				log.Warnf("firmware: re-present after mount failed: %v", err)
			}
		}
	}()

	if err := c.mountLocked(); err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	defer func() {
		if err := c.unmountLocked(); err != nil {
			log.Warnf("firmware: deferred unmount failed: %v", err)
		}
	}()

	return fn()
}

// attachLoopLocked attaches c.imagePath to a free loop device with
// partition scanning enabled. Idempotent. Must hold c.mu.
func (c *Controller) attachLoopLocked() error {
	if dev := c.findLoopDevForImage(); dev != "" {
		c.loopDev = dev
		log.Debugf("firmware: loop device %s already attached to %s", dev, c.imagePath)
		return nil
	}

	out, err := exec.Command("losetup", "-f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("losetup -f: %s: %w", strings.TrimSpace(string(out)), err)
	}
	dev := strings.TrimSpace(string(out))
	if dev == "" {
		return fmt.Errorf("losetup -f returned empty device path")
	}

	if out, err := exec.Command("losetup", "-P", dev, c.imagePath).CombinedOutput(); err != nil {
		return fmt.Errorf("losetup -P %s %s: %s: %w", dev, c.imagePath, strings.TrimSpace(string(out)), err)
	}

	// Wait briefly for the kernel to expose <loop>pN partition nodes.
	partDev := dev + "p1"
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(partDev); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	c.loopDev = dev
	log.Infof("firmware: attached loop device %s for %s", dev, c.imagePath)
	return nil
}

// detachLoopLocked detaches the persistent loop device for the image.
// Used during image replacement (download) and on shutdown. Retries
// because BusyBox umount -d's auto-detach is asynchronous on some
// kernels. Must hold c.mu.
func (c *Controller) detachLoopLocked() error {
	const attempts = 10
	for i := 0; i < attempts; i++ {
		dev := c.findLoopDevForImage()
		if dev == "" {
			c.loopDev = ""
			return nil
		}
		out, err := exec.Command("losetup", "-d", dev).CombinedOutput()
		if err == nil {
			time.Sleep(50 * time.Millisecond)
			if c.findLoopDevForImage() == "" {
				c.loopDev = ""
				return nil
			}
		} else {
			log.Debugf("firmware: losetup -d %s attempt %d: %s", dev, i+1, strings.TrimSpace(string(out)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("loop device for %s still attached after %d attempts", c.imagePath, attempts)
}

// mountLocked mounts the (already-attached) loop partition at c.mountPoint.
// Idempotent. Must hold c.mu and c.loopDev must be non-empty.
func (c *Controller) mountLocked() error {
	if c.mountPoint == "" {
		return fmt.Errorf("mountPoint not configured")
	}
	if err := os.MkdirAll(c.mountPoint, 0o755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}
	if isMounted(c.mountPoint) {
		return nil
	}

	partDev := c.loopDev + "p1"
	if _, err := os.Stat(partDev); err != nil {
		log.Warnf("firmware: %s not found, falling back to raw loop mount", partDev)
		partDev = c.loopDev
	}

	out, err := exec.Command("mount", "-t", "vfat", "-o", "rw,sync", partDev, c.mountPoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount %s: %s: %w", partDev, strings.TrimSpace(string(out)), err)
	}
	log.Debugf("firmware: mounted %s at %s", partDev, c.mountPoint)
	return nil
}

// unmountLocked unmounts c.mountPoint and flushes caches so the gadget
// re-reads fresh bytes from disk on its next access. Leaves the loop
// device attached. Must hold c.mu.
func (c *Controller) unmountLocked() error {
	if !isMounted(c.mountPoint) {
		return nil
	}
	// Flush dirty pages so the on-disk image reflects this write window.
	_ = exec.Command("sync").Run()

	out, err := exec.Command("umount", c.mountPoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("umount: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Invalidate page cache so f_mass_storage doesn't serve stale pages
	// the next time the host issues a READ on the gadget LUN.
	_ = os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0o644)

	log.Debug("firmware: unmounted and flushed caches")
	return nil
}

// isMounted checks /proc/mounts for the given path.
func isMounted(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

// findLoopDevForImage scans /sys/block for a loop device backing c.imagePath.
func (c *Controller) findLoopDevForImage() string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "loop") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/sys/block", e.Name(), "loop", "backing_file"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == c.imagePath {
			return "/dev/" + e.Name()
		}
	}
	return ""
}
