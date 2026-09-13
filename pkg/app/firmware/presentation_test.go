package firmware

// presentation_test.go pins when the capsule volume is attached to the
// gadget's lun.0. The rule is that the host only ever sees the volume while
// at least one capsule is actually waiting to be applied: an empty
// \EFI\UpdateCapsule\ is nothing for firmware to find, and a permanently
// attached LUN just adds a device the host has to enumerate and scan at
// every boot.

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
)

// presentationController builds a Controller with both a capsule volume and a
// fake gadget, so the presentation decisions are observable without configfs.
func presentationController(t *testing.T) (*Controller, *fakeVMGadget) {
	t.Helper()
	dir := t.TempDir()
	c := &Controller{
		capsulePath:     filepath.Join(dir, "capsules.img"),
		capsuleSize:     capsuleVolumeBytes(capsuleMinSizeMB),
		mediaDir:        dir,
		stagingSentinel: filepath.Join(dir, ".staging"),
		log:             slog.New(slog.DiscardHandler),
	}
	g := &fakeVMGadget{}
	c.SetGadgetForTest(g)
	return c, g
}

// hostDeletesCapsule removes a capsule from the volume behind the Controller's
// back, the way host firmware does once it has applied one.
func hostDeletesCapsule(t *testing.T, volumePath, name string) {
	t.Helper()
	d, err := diskfs.Open(volumePath, diskfs.WithOpenMode(diskfs.ReadWrite))
	if err != nil {
		t.Fatalf("open volume: %v", err)
	}
	fs, err := d.GetFilesystem(1)
	if err != nil {
		t.Fatalf("GetFilesystem(1): %v", err)
	}
	if err := fs.Remove(capsuleDirPath + "/" + name); err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close volume: %v", err)
	}
}

func TestInitDoesNotPresentVolumeWhenNoCapsulesPending(t *testing.T) {
	c, g := presentationController(t)

	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if g.lun0 != "" {
		t.Errorf("lun.0 backing file = %q, want empty (nothing staged)", g.lun0)
	}
	if c.presented {
		t.Error("c.presented = true, want false with an empty capsule volume")
	}
}

func TestStagingCapsulePresentsVolume(t *testing.T) {
	c, g := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := c.StageCapsule("fw.cap", strings.NewReader("payload")); err != nil {
		t.Fatalf("StageCapsule: %v", err)
	}

	if g.lun0 != c.capsulePath {
		t.Errorf("lun.0 backing file = %q, want %q", g.lun0, c.capsulePath)
	}
	if !c.presented {
		t.Error("c.presented = false, want true with a capsule staged")
	}
}

func TestInitPresentsVolumeWhenCapsuleSurvivedRestart(t *testing.T) {
	c, _ := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.StageCapsule("fw.cap", strings.NewReader("payload")); err != nil {
		t.Fatalf("StageCapsule: %v", err)
	}

	// A BMC restart: same volume on disk, a fresh Controller and gadget.
	restarted := &Controller{
		capsulePath:     c.capsulePath,
		capsuleSize:     c.capsuleSize,
		mediaDir:        c.mediaDir,
		stagingSentinel: c.stagingSentinel,
		log:             slog.New(slog.DiscardHandler),
	}
	g2 := &fakeVMGadget{}
	restarted.SetGadgetForTest(g2)

	if err := restarted.Init(context.Background()); err != nil {
		t.Fatalf("Init after restart: %v", err)
	}

	if g2.lun0 != restarted.capsulePath {
		t.Errorf("lun.0 after restart = %q, want %q", g2.lun0, restarted.capsulePath)
	}
}

func TestClearingLastCapsuleUnpresentsVolume(t *testing.T) {
	c, g := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.StageCapsule("fw.cap", strings.NewReader("payload")); err != nil {
		t.Fatalf("StageCapsule: %v", err)
	}

	if err := c.ClearCapsules(); err != nil {
		t.Fatalf("ClearCapsules: %v", err)
	}

	if g.lun0 != "" {
		t.Errorf("lun.0 backing file = %q, want empty after clearing", g.lun0)
	}
	if c.presented {
		t.Error("c.presented = true, want false after clearing the last capsule")
	}
}

func TestRemovingOneOfTwoCapsulesKeepsVolumePresented(t *testing.T) {
	c, g := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, name := range []string{"a.cap", "b.cap"} {
		if _, err := c.StageCapsule(name, strings.NewReader("payload")); err != nil {
			t.Fatalf("StageCapsule(%q): %v", name, err)
		}
	}

	if err := c.RemoveCapsule("a.cap"); err != nil {
		t.Fatalf("RemoveCapsule: %v", err)
	}

	if g.lun0 != c.capsulePath {
		t.Errorf("lun.0 = %q, want still %q with b.cap pending", g.lun0, c.capsulePath)
	}
}

func TestStatusUnpresentsVolumeAfterHostAppliesLastCapsule(t *testing.T) {
	c, g := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.StageCapsule("fw.cap", strings.NewReader("payload")); err != nil {
		t.Fatalf("StageCapsule: %v", err)
	}

	hostDeletesCapsule(t, c.capsulePath, "fw.cap")

	st := c.GetStatus()

	if len(st.Capsules) != 0 {
		t.Fatalf("Status.Capsules = %+v, want empty after the host applied it", st.Capsules)
	}
	if st.Presented {
		t.Error("Status.Presented = true, want false once nothing is pending")
	}
	if g.lun0 != "" {
		t.Errorf("lun.0 backing file = %q, want empty once nothing is pending", g.lun0)
	}
}

func TestClearingCapsulesDoesNotReattachVolume(t *testing.T) {
	c, g := presentationController(t)
	if err := c.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.StageCapsule("fw.cap", strings.NewReader("payload")); err != nil {
		t.Fatalf("StageCapsule: %v", err)
	}
	g.calls = nil

	if err := c.ClearCapsules(); err != nil {
		t.Fatalf("ClearCapsules: %v", err)
	}

	// Emptying the volume must not make the host watch the media come back
	// before it goes away again: withVolume unpresents for the write, and the
	// re-presentation it would normally do has to be suppressed once the last
	// capsule is gone.
	for _, call := range g.calls {
		if call == "present" {
			t.Errorf("gadget calls = %v, want no re-present while emptying the volume", g.calls)
			break
		}
	}
}
