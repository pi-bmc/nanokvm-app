package firmware

// virtual_media.go implements virtual media management.
//
// Workflow:
//   1. One or more ISO files are staged in c.mediaDir (upload / fetch).
//   2. The user selects one to insert.  InsertVirtualMedia writes the staged
//      file path directly to the USB gadget's lun.1 configfs attribute.
//      The managed server sees a USB CD-ROM without the ISO being copied
//      anywhere — no writes to the firmware FAT image at all.
//   3. Ejecting clears lun.1/file so the host sees an empty tray.
//
// This avoids the "no space left" problem of trying to write an ISO into the
// small FAT partition that is already full with U-Boot firmware files.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

// VirtualMediaState describes the current ISO insertion state.
type VirtualMediaState struct {
	Inserted  bool   `json:"inserted"`
	ImageName string `json:"imageName,omitempty"` // original filename chosen by the user
	ImageSize int64  `json:"imageSize,omitempty"` // size in bytes
}

// GetVirtualMediaState returns the current virtual media state.
func (c *Controller) GetVirtualMediaState() VirtualMediaState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vmState
}

// GetMediaDir returns the path to the ISO staging directory.
func (c *Controller) GetMediaDir() string { return c.mediaDir }

// mediaPathFor returns the absolute path of name inside the media dir and
// verifies the result doesn't escape outside it.
func (c *Controller) mediaPathFor(name string) (string, error) {
	clean := filepath.Base(name)
	if clean == "" || clean == "." {
		return "", fmt.Errorf("invalid media filename %q", name)
	}
	return filepath.Join(c.mediaDir, clean), nil
}

// ListMediaFiles returns the filenames present in the media staging directory.
func (c *Controller) ListMediaFiles() ([]string, error) {
	if c.mediaDir == "" {
		return nil, fmt.Errorf("mediaDir not configured")
	}
	entries, err := os.ReadDir(c.mediaDir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// SaveMediaFile writes r to mediaDir/<name>, creating the directory if needed.
// It returns the number of bytes saved.
func (c *Controller) SaveMediaFile(name string, r io.Reader) (int64, error) {
	if c.mediaDir == "" {
		return 0, fmt.Errorf("mediaDir not configured")
	}
	destPath, err := c.mediaPathFor(name)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(c.mediaDir, 0o755); err != nil {
		return 0, fmt.Errorf("create media dir: %w", err)
	}
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, r)
	syncErr := f.Sync()
	_ = f.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("write media file: %w", copyErr)
	}
	if syncErr != nil {
		return n, fmt.Errorf("sync media file: %w", syncErr)
	}
	log.Infof("firmware: saved media file %q (%d bytes)", name, n)
	return n, nil
}

// DeleteMediaFile removes name from the media staging directory.
// Returns an error if that file is currently inserted.
func (c *Controller) DeleteMediaFile(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vmState.Inserted && c.vmState.ImageName == name {
		return fmt.Errorf("cannot delete %q: currently inserted; eject first", name)
	}
	destPath, err := c.mediaPathFor(name)
	if err != nil {
		return err
	}
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InsertVirtualMedia presents the named ISO from mediaDir as a USB CD-ROM
// via the gadget's lun.1. The ISO is NOT copied anywhere — the gadget reads
// it directly from mediaDir, so no space in the firmware FAT is consumed.
func (c *Controller) InsertVirtualMedia(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.vmState.Inserted {
		return fmt.Errorf("virtual media already inserted (%s); eject first", c.vmState.ImageName)
	}

	srcPath, err := c.mediaPathFor(name)
	if err != nil {
		return err
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("media file %q not found: %w", name, err)
	}

	if err := c.presentISO(srcPath); err != nil {
		return fmt.Errorf("insert virtual media: %w", err)
	}

	c.vmState = VirtualMediaState{Inserted: true, ImageName: name, ImageSize: info.Size()}
	log.Infof("firmware: inserted virtual media %q (%d bytes) via lun.1", name, info.Size())
	return nil
}

// EjectVirtualMedia clears lun.1 so the managed server sees an empty CD-ROM tray.
func (c *Controller) EjectVirtualMedia() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.vmState.Inserted {
		return nil // idempotent
	}

	prevName := c.vmState.ImageName

	if err := c.unpresentISO(); err != nil {
		return fmt.Errorf("eject virtual media: %w", err)
	}

	c.vmState = VirtualMediaState{}
	log.Infof("firmware: ejected virtual media %s", prevName)
	return nil
}
