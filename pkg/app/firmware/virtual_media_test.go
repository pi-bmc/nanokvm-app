package firmware

// virtual_media_test.go pins the two media lifecycles and the machinery that
// keeps them honest across restarts.
//
// A persistent insert (UI / /api/firmware) treats the media directory as a
// library: eject leaves the file. An ephemeral insert (Redfish, which has no
// delete verb) ties the file's life to the mount: eject deletes it. The
// contract is recorded in an on-disk marker because the insertion itself
// survives a BMC restart via configfs, and the startup sweep finishes any
// eject a crash interrupted.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/pi-bmc/nanokvm-app/pkg/config"
)

// fakeVMGadget stands in for the configfs-backed gadget, which does not exist
// off-device. lun1 mirrors what the kernel would persist across a restart, and
// lun0 the capsule volume's backing file. calls records the capsule LUN's
// attach/detach sequence, so a test can assert not just the end state but that
// the host was not made to watch the media flap on the way there.
type fakeVMGadget struct {
	lun1  string
	lun0  string
	calls []string
}

func (g *fakeVMGadget) InsertMedia(path string) error { g.lun1 = path; return nil }
func (g *fakeVMGadget) EjectMedia() error             { g.lun1 = ""; return nil }
func (g *fakeVMGadget) LUN1File() (string, bool)      { return g.lun1, g.lun1 != "" }

func (g *fakeVMGadget) PresentDisk(_ context.Context, path string) error {
	g.lun0 = path
	g.calls = append(g.calls, "present")
	return nil
}

func (g *fakeVMGadget) UnpresentDisk() error {
	g.lun0 = ""
	g.calls = append(g.calls, "unpresent")
	return nil
}

func mediaController(t *testing.T) (*Controller, *fakeVMGadget, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Firmware.MediaDir = dir
	c := NewController(cfg, slog.New(slog.DiscardHandler))
	g := &fakeVMGadget{}
	c.SetGadgetForTest(g)
	return c, g, dir
}

func stageFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("ISO"), 0o644); err != nil {
		t.Fatalf("stage %q: %v", name, err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat %q: %v", path, err)
	}
	return err == nil
}

func TestEphemeralInsertDeletesFileOnEject(t *testing.T) {
	c, g, dir := mediaController(t)
	stageFile(t, dir, "cloud.iso")

	if err := c.InsertVirtualMediaEphemeral("cloud.iso"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if st := c.GetVirtualMediaState(); !st.Inserted || !st.Ephemeral {
		t.Fatalf("state = %+v, want inserted and ephemeral", st)
	}
	if !exists(t, filepath.Join(dir, ".cloud.iso.ephemeral")) {
		t.Error("ephemeral marker not written at insert")
	}

	if err := c.EjectVirtualMedia(); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if g.lun1 != "" {
		t.Error("gadget lun.1 not cleared")
	}
	if exists(t, filepath.Join(dir, "cloud.iso")) {
		t.Error("ephemeral media must be deleted on eject")
	}
	if exists(t, filepath.Join(dir, ".cloud.iso.ephemeral")) {
		t.Error("marker must be removed with the media")
	}
}

func TestPersistentInsertSurvivesEject(t *testing.T) {
	c, _, dir := mediaController(t)
	stageFile(t, dir, "library.iso")

	if err := c.InsertVirtualMedia("library.iso"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if st := c.GetVirtualMediaState(); st.Ephemeral {
		t.Fatalf("state = %+v, want persistent", st)
	}
	if err := c.EjectVirtualMedia(); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if !exists(t, filepath.Join(dir, "library.iso")) {
		t.Error("persistent media must survive eject")
	}
}

// A persistent re-insert of a name that once carried the ephemeral contract
// renews the file's tenure: the stale marker must not delete it later.
func TestPersistentReinsertClearsStaleMarker(t *testing.T) {
	c, _, dir := mediaController(t)
	stageFile(t, dir, "keep.iso")
	stageFile(t, dir, ".keep.iso.ephemeral")

	if err := c.InsertVirtualMedia("keep.iso"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := c.EjectVirtualMedia(); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if !exists(t, filepath.Join(dir, "keep.iso")) {
		t.Error("stale marker deleted a persistently re-inserted file")
	}
	if exists(t, filepath.Join(dir, ".keep.iso.ephemeral")) {
		t.Error("persistent insert must clear the stale marker")
	}
}

// The insertion outlives the process (configfs persists lun.1), so the
// ephemeral contract must be recoverable by a fresh controller.
func TestEphemeralContractSurvivesRestart(t *testing.T) {
	c1, g, dir := mediaController(t)
	stageFile(t, dir, "cloud.iso")
	if err := c1.InsertVirtualMediaEphemeral("cloud.iso"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// New controller, same media dir and same gadget state: a restart.
	cfg := &config.Config{}
	cfg.Firmware.MediaDir = dir
	c2 := NewController(cfg, slog.New(slog.DiscardHandler))
	c2.SetGadgetForTest(g)

	st := c2.GetVirtualMediaState()
	if !st.Inserted || st.ImageName != "cloud.iso" || !st.Ephemeral {
		t.Fatalf("recovered state = %+v, want inserted ephemeral cloud.iso", st)
	}
	if err := c2.EjectVirtualMedia(); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if exists(t, filepath.Join(dir, "cloud.iso")) {
		t.Error("ephemeral media must be deleted on eject after a restart")
	}
}

func TestSweepFinishesInterruptedEject(t *testing.T) {
	c, g, dir := mediaController(t)

	// An orphan: marker present, nothing inserted — a crash between gadget
	// eject and file removal.
	stageFile(t, dir, "orphan.iso")
	stageFile(t, dir, ".orphan.iso.ephemeral")

	// A live mount: its marker and file must be spared.
	stageFile(t, dir, "live.iso")
	stageFile(t, dir, ".live.iso.ephemeral")
	g.lun1 = filepath.Join(dir, "live.iso")

	c.SweepEphemeralMedia()

	if exists(t, filepath.Join(dir, "orphan.iso")) || exists(t, filepath.Join(dir, ".orphan.iso.ephemeral")) {
		t.Error("sweep must remove orphaned ephemeral media and its marker")
	}
	if !exists(t, filepath.Join(dir, "live.iso")) || !exists(t, filepath.Join(dir, ".live.iso.ephemeral")) {
		t.Error("sweep must spare the currently-inserted image")
	}
}

func TestListMediaFilesHidesMarkers(t *testing.T) {
	c, _, dir := mediaController(t)
	stageFile(t, dir, "visible.iso")
	stageFile(t, dir, ".visible.iso.ephemeral")

	names, err := c.ListMediaFiles()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 1 || names[0] != "visible.iso" {
		t.Errorf("ListMediaFiles = %v, want only visible.iso", names)
	}
}
