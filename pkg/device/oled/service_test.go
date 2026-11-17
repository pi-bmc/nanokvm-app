package oled

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pi-bmc/nanokvm-app/pkg/config"
	"github.com/pi-bmc/nanokvm-app/pkg/device/oled/ssd1306"
)

// SSD1306 bytes the tests look for on the wire.
const (
	ctrlCommand = 0x00
	ctrlData    = 0x40
	cmdOn       = 0xAF
	cmdOff      = 0xAE
	cmdContrast = 0x81
	cmdMux      = 0xA8 // only ever sent by Init
)

func testConfig() config.Display {
	return config.Display{
		Enabled:         true,
		Address:         "0x3c",
		Brightness:      0x8F,
		PageSeconds:     5,
		DimAfterMinutes: 10,
	}
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fakeBus records transfers and can stop answering, like a module being
// pulled off its header. Safe for the service goroutine and the test to
// share.
type fakeBus struct {
	mu        sync.Mutex
	transfers [][]byte
	closed    bool
	err       error
}

func (b *fakeBus) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	b.transfers = append(b.transfers, bytes.Clone(p))
	return len(p), nil
}

func (b *fakeBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func (b *fakeBus) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *fakeBus) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transfers = nil
}

func (b *fakeBus) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// commands is every command byte sent so far, in order.
func (b *fakeBus) commands() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []byte
	for _, t := range b.transfers {
		if t[0] == ctrlCommand {
			out = append(out, t[1:]...)
		}
	}
	return out
}

// frames is how many whole-panel data transfers were sent.
func (b *fakeBus) fullFrames() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, t := range b.transfers {
		if t[0] == ctrlData && len(t) == 1+ssd1306.BufferSize {
			n++
		}
	}
	return n
}

// rig is a Service wired to a fake bus, with its panel stepped by hand so
// the tests control time instead of sleeping through it.
type rig struct {
	svc   *Service
	panel *panel
	bus   *fakeBus
	opens int
	// opened is the device path the last open was asked for.
	opened string
	// openErr, when set, is what opening the bus returns.
	openErr error
	st      Status
	t0      time.Time
}

func newRig(t *testing.T, cfg config.Display) *rig {
	t.Helper()

	r := &rig{bus: &fakeBus{}, st: sampleStatus(), t0: time.Unix(1_700_000_000, 0)}
	r.svc = New(cfg, func(context.Context) Status { return r.st }, quietLog())
	r.svc.findBus = func() (string, error) { return "/dev/i2c-3", nil }
	r.svc.open = func(device string, _ uint8) (ssd1306.Bus, error) {
		r.opens++
		r.opened = device
		if r.openErr != nil {
			return nil, r.openErr
		}
		return r.bus, nil
	}

	addr, err := ParseAddress(cfg.Address)
	if err != nil {
		t.Fatal(err)
	}
	r.svc.addr = addr
	r.panel = &panel{svc: r.svc, dir: newDirector(cfg, r.t0)}
	return r
}

func (r *rig) tick(after time.Duration) bool {
	return r.panel.tick(context.Background(), r.t0.Add(after))
}

func TestFirstTickProgramsPaintsThenLights(t *testing.T) {
	r := newRig(t, testConfig())

	if !r.tick(0) {
		t.Fatal("tick ended the loop with a panel present")
	}

	// The order is the point: configuration, then the picture, then
	// display-on — so the glass never shows the controller's power-on RAM.
	tr := r.bus.transfers
	if len(tr) != 4 {
		t.Fatalf("first tick used %d transfers, want 4 (init, window, frame, on)", len(tr))
	}
	if tr[0][0] != ctrlCommand || !bytes.Contains(tr[0], []byte{cmdMux}) {
		t.Errorf("transfer 0 is not the init sequence: % X", tr[0])
	}
	if tr[2][0] != ctrlData || len(tr[2]) != 1+ssd1306.BufferSize {
		t.Errorf("transfer 2 is not a full frame (%d bytes)", len(tr[2]))
	}
	if !bytes.Equal(tr[3], []byte{ctrlCommand, cmdOn}) {
		t.Errorf("transfer 3 = % X, want display-on", tr[3])
	}

	want := Render(r.st, PageNetwork, Layout{})
	if !bytes.Equal(tr[2][1:], want[:]) {
		t.Error("the frame on the wire is not the network page")
	}
}

func TestSteadyStateIsSilent(t *testing.T) {
	r := newRig(t, testConfig())
	r.tick(0)
	r.bus.reset()

	// Same status, same page, same orbit step: nothing to say.
	r.tick(1 * time.Second)
	r.tick(2 * time.Second)
	if n := len(r.bus.transfers); n != 0 {
		t.Errorf("two idle ticks used %d transfers, want 0", n)
	}
}

func TestMissingBusEndsTheLoop(t *testing.T) {
	r := newRig(t, testConfig())
	r.openErr = &fs.PathError{Op: "open", Path: "/dev/i2c-3", Err: fs.ErrNotExist}

	if r.tick(0) {
		t.Error("tick wants to keep going on a kernel with no such bus")
	}
}

// With no device configured the bus is the one FindBus resolves, and a
// configured device is used as written without consulting it.
func TestDeviceDefaultsToTheBoardsDisplayBus(t *testing.T) {
	r := newRig(t, testConfig())
	r.svc.findBus = func() (string, error) { return "/dev/i2c-7", nil }
	r.tick(0)
	if r.opened != "/dev/i2c-7" {
		t.Errorf("opened %q, want the bus FindBus resolved", r.opened)
	}

	cfg := testConfig()
	cfg.Device = "/dev/i2c-1"
	r = newRig(t, cfg)
	r.svc.findBus = func() (string, error) {
		t.Error("FindBus consulted although a device was configured")
		return "", fs.ErrNotExist
	}
	r.tick(0)
	if r.opened != "/dev/i2c-1" {
		t.Errorf("opened %q, want the configured device", r.opened)
	}
}

// A machine without the board's display controller — an older image, or a
// developer's workstation — must end up touching no bus at all.
func TestNoControllerMeansNoBusIsOpened(t *testing.T) {
	r := newRig(t, testConfig())
	r.svc.findBus = func() (string, error) { return FindBus(t.TempDir(), Controller) }

	if r.tick(0) {
		t.Error("tick wants to keep going without the display controller")
	}
	if r.opens != 0 {
		t.Errorf("opened a bus %d times with no controller present", r.opens)
	}
}

func TestFindBus(t *testing.T) {
	root := t.TempDir()

	if _, err := FindBus(root, Controller); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no controller: err = %v, want fs.ErrNotExist", err)
	}

	// The controller exists but registered no adapter (status = "disabled").
	if err := os.MkdirAll(filepath.Join(root, Controller, "power"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindBus(root, Controller); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller without adapter: err = %v, want fs.ErrNotExist", err)
	}

	// Whatever number the kernel gave it is the one to open.
	if err := os.MkdirAll(filepath.Join(root, Controller, "i2c-5"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A neighbouring controller's adapter must not be picked up.
	if err := os.MkdirAll(filepath.Join(root, "4040000.i2c", "i2c-4"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindBus(root, Controller)
	if err != nil || got != "/dev/i2c-5" {
		t.Errorf("FindBus = %q, %v; want /dev/i2c-5", got, err)
	}
}

func TestAbsentPanelIsProbedOnASlowerCadence(t *testing.T) {
	r := newRig(t, testConfig())
	r.bus.fail(unix.EREMOTEIO)

	if !r.tick(0) {
		t.Fatal("an empty connector ended the loop; it should keep probing")
	}
	if r.opens != 1 {
		t.Fatalf("opens = %d, want 1", r.opens)
	}
	if !r.bus.isClosed() {
		t.Error("the bus was left open after a failed probe")
	}

	// Ticks inside the probe interval must not touch the bus again.
	r.tick(1 * time.Second)
	r.tick(probeEvery - time.Second)
	if r.opens != 1 {
		t.Errorf("opens = %d before the probe interval elapsed, want 1", r.opens)
	}

	// The module is plugged in; the next probe finds it and lights it.
	r.bus.fail(nil)
	r.tick(probeEvery)
	if r.opens != 2 {
		t.Fatalf("opens = %d after the probe interval, want 2", r.opens)
	}
	if !bytes.Contains(r.bus.commands(), []byte{cmdOn}) {
		t.Error("hot-plugged panel was not switched on")
	}
}

func TestUnpluggedPanelIsReleasedAndFoundAgain(t *testing.T) {
	r := newRig(t, testConfig())
	r.tick(0)

	// Pull the module, and change the picture so the next tick has to talk
	// to it — an unchanged frame sends nothing and would not notice.
	r.bus.fail(unix.EREMOTEIO)
	r.st.Net.Addr = "10.0.0.9"
	if !r.tick(time.Second) {
		t.Fatal("a failed paint ended the loop")
	}
	if r.panel.dev != nil {
		t.Fatal("the dead panel was not released")
	}

	r.bus.fail(nil)
	r.bus.reset()
	r.tick(time.Second + probeEvery)
	if r.panel.dev == nil {
		t.Fatal("the panel was not picked up again")
	}
	if r.bus.fullFrames() != 1 || !bytes.Contains(r.bus.commands(), []byte{cmdOn}) {
		t.Error("the returning panel was not fully repainted and switched on")
	}
}

// A module that browned out comes back dark with random RAM, still
// acknowledging every write. Nothing reports that, so the service reprograms
// and repaints on a timer whether or not anything looks wrong.
func TestPanelIsReassertedPeriodically(t *testing.T) {
	r := newRig(t, testConfig())
	r.tick(0)
	r.bus.reset()

	r.tick(reassertEvery - time.Second)
	if bytes.Contains(r.bus.commands(), []byte{cmdMux}) {
		t.Fatal("panel reprogrammed before the reassert interval")
	}

	r.bus.reset()
	r.tick(reassertEvery)
	cmds := r.bus.commands()
	if !bytes.Contains(cmds, []byte{cmdMux}) {
		t.Error("panel was not reprogrammed at the reassert interval")
	}
	if r.bus.fullFrames() != 1 {
		t.Errorf("reassert sent %d full frames, want 1", r.bus.fullFrames())
	}
	if !bytes.Contains(cmds, []byte{cmdOn}) {
		t.Error("reassert did not switch the panel back on")
	}
	if bytes.Contains(cmds, []byte{cmdOff}) {
		t.Error("reassert switched the panel off; a healthy panel would blink every minute")
	}
}

// The minute's reassert reprograms the panel. On a dimmed display it must
// come back dim: programming the configured brightness and then dimming again
// would be a visible flash once a minute, all night.
func TestReassertKeepsADimmedPanelDim(t *testing.T) {
	cfg := testConfig()
	cfg.DimAfterMinutes = 1
	r := newRig(t, cfg)
	r.tick(0)
	r.tick(time.Minute) // dims, and reasserts for the first time
	r.bus.reset()

	r.tick(2 * time.Minute) // second reassert, already dim
	cmds := r.bus.commands()
	if !bytes.Contains(cmds, []byte{cmdMux}) {
		t.Fatal("no reassert happened; the test is not exercising what it claims")
	}
	if bytes.Contains(cmds, []byte{cmdContrast, brightness(cfg)}) {
		t.Errorf("reassert programmed full brightness on a dimmed panel: % X", cmds)
	}
	if !bytes.Contains(cmds, []byte{cmdContrast, dimContrast}) {
		t.Errorf("reassert did not program the dim level: % X", cmds)
	}
}

func TestDimContrastReachesThePanel(t *testing.T) {
	cfg := testConfig()
	cfg.DimAfterMinutes = 1
	r := newRig(t, cfg)
	r.tick(0)
	r.bus.reset()

	// Past the dim deadline but short of the reassert, so the only reason
	// to send a contrast command is the dimming itself.
	r.panel.reassertAt = r.t0.Add(time.Hour)
	r.tick(time.Minute)
	if !bytes.Contains(r.bus.commands(), []byte{cmdContrast, dimContrast}) {
		t.Errorf("idle panel was not dimmed: % X", r.bus.commands())
	}

	// A power transition brings it straight back.
	r.bus.reset()
	r.st.Host.Power = PowerOff
	r.tick(time.Minute + time.Second)
	if !bytes.Contains(r.bus.commands(), []byte{cmdContrast, brightness(cfg)}) {
		t.Errorf("event did not restore brightness: % X", r.bus.commands())
	}
}

func TestStartStopLifecycle(t *testing.T) {
	bus := &fakeBus{}
	svc := New(testConfig(), func(context.Context) Status { return sampleStatus() }, quietLog())
	svc.findBus = func() (string, error) { return "/dev/i2c-3", nil }
	svc.open = func(string, uint8) (ssd1306.Bus, error) { return bus, nil }
	svc.tick = time.Millisecond

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Contains(bus.commands(), []byte{cmdOn}) {
		if time.Now().After(deadline) {
			t.Fatal("panel was never switched on")
		}
		time.Sleep(time.Millisecond)
	}

	svc.Stop()

	// A display that outlives the process drawing it would go on showing
	// a power state nobody is maintaining; Stop has to leave it dark.
	cmds := bus.commands()
	if len(cmds) == 0 || cmds[len(cmds)-1] != cmdOff {
		t.Errorf("last command after Stop = % X, want display-off", cmds)
	}
	if !bus.isClosed() {
		t.Error("Stop did not release the bus")
	}

	svc.Stop() // idempotent
}

func TestStopFollowsContextCancellation(t *testing.T) {
	bus := &fakeBus{}
	svc := New(testConfig(), func(context.Context) Status { return sampleStatus() }, quietLog())
	svc.findBus = func() (string, error) { return "/dev/i2c-3", nil }
	svc.open = func(string, uint8) (ssd1306.Bus, error) { return bus, nil }
	svc.tick = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	svc.Stop() // must return: the goroutine exits on the cancelled context

	if !bus.isClosed() && len(bus.commands()) > 0 {
		t.Error("bus opened but not released after cancellation")
	}
}

func TestDisabledDisplayNeverOpensTheBus(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false

	opened := false
	svc := New(cfg, func(context.Context) Status { return Status{} }, quietLog())
	svc.open = func(string, uint8) (ssd1306.Bus, error) {
		opened = true
		return &fakeBus{}, nil
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	svc.Stop()
	if opened {
		t.Error("a disabled display opened the bus")
	}
}

func TestStartRejectsBadAddress(t *testing.T) {
	cfg := testConfig()
	cfg.Address = "0x3g"

	svc := New(cfg, func(context.Context) Status { return Status{} }, quietLog())
	if err := svc.Start(context.Background()); err == nil {
		t.Error("Start accepted an unparseable address")
	}
	svc.Stop() // never started; must not block
}

func TestParseAddress(t *testing.T) {
	good := map[string]uint8{"0x3c": 0x3C, "0x3C": 0x3C, "0x3d": 0x3D, "60": 0x3C, "0x08": 0x08, "0x77": 0x77}
	for in, want := range good {
		got, err := ParseAddress(in)
		if err != nil || got != want {
			t.Errorf("ParseAddress(%q) = %#x, %v; want %#x", in, got, err, want)
		}
	}
	// 0x78 is what the module's silkscreen says (the 8-bit form of 0x3c);
	// it is reserved as a 7-bit address and must be refused, not shifted.
	for _, in := range []string{"", "x", "0x78", "0x07", "0", "256", "-1"} {
		if _, err := ParseAddress(in); err == nil {
			t.Errorf("ParseAddress(%q) succeeded", in)
		}
	}
}

// --- director ---------------------------------------------------------------

type directorRig struct {
	d  *director
	t0 time.Time
	st Status
}

func newDirectorRig(cfg config.Display) *directorRig {
	t0 := time.Unix(1_700_000_000, 0)
	return &directorRig{d: newDirector(cfg, t0), t0: t0, st: sampleStatus()}
}

func (r *directorRig) step(after time.Duration) (Frame, uint8) {
	return r.d.step(r.t0.Add(after), r.st)
}

// at is the frame the director should produce for a page at an offset from
// the start, orbit included.
func (r *directorRig) want(after time.Duration, f Frame) Frame {
	o := orbit[int(after/orbitEvery)%len(orbit)]
	return f.shift(o.X, o.Y)
}

func TestDirectorRotatesPages(t *testing.T) {
	r := newDirectorRig(testConfig())

	steps := []struct {
		after time.Duration
		page  Page
	}{
		{0, PageNetwork},
		{4 * time.Second, PageNetwork},
		{5 * time.Second, PageHost},
		{9 * time.Second, PageHost},
		{10 * time.Second, PageBMC},
		{15 * time.Second, PageNetwork}, // wraps
	}
	for _, s := range steps {
		got, _ := r.step(s.after)
		if got != Render(r.st, s.page, Layout{}) {
			t.Errorf("at %v: not showing page %s", s.after, pageName(s.page))
		}
	}
}

func TestDirectorSplashesOnPowerTransition(t *testing.T) {
	r := newDirectorRig(testConfig())
	r.step(0)

	r.st.Host.Power = PowerOff
	got, _ := r.step(time.Second)
	if got != RenderSplash(r.st, "POWER OFF", Layout{}) {
		t.Fatal("power-off did not bring up the splash")
	}
	if got, _ := r.step(time.Second + splashFor - time.Second); got != RenderSplash(r.st, "POWER OFF", Layout{}) {
		t.Error("splash did not hold for its duration")
	}

	// After the splash, the host page — and for a full page interval,
	// not whatever was left of the page the splash interrupted.
	after := time.Second + splashFor
	if got, _ := r.step(after); got != Render(r.st, PageHost, Layout{}) {
		t.Error("splash did not give way to the host page")
	}
	if got, _ := r.step(after + 4*time.Second); got != Render(r.st, PageHost, Layout{}) {
		t.Error("host page after a splash was cut short")
	}

	r.st.Host.Power = PowerOn
	if got, _ := r.step(after + 5*time.Second); got != RenderSplash(r.st, "POWER ON", Layout{}) {
		t.Error("power-on did not bring up the splash")
	}
}

// The sense line appearing or failing is not the host changing state.
func TestDirectorIgnoresTransitionsThroughUnknown(t *testing.T) {
	r := newDirectorRig(testConfig())
	r.st.Host.Power = PowerUnknown
	r.step(0)

	r.st.Host.Power = PowerOn
	if got, _ := r.step(time.Second); got != Render(r.st, PageNetwork, Layout{}) {
		t.Error("unknown -> on raised a splash")
	}
}

func TestDirectorJumpsToAlertsAndLeavesWhenClear(t *testing.T) {
	r := newDirectorRig(testConfig())
	r.step(0)

	healthy := r.st
	r.st = alertStatus()
	if got, _ := r.step(time.Second); got != Render(r.st, PageAlerts, Layout{}) {
		t.Fatal("a new alert did not bring up the alerts page")
	}

	// Cleared: the alerts page is no longer in the rotation, so the
	// display must move off it at once rather than show an empty list.
	r.st = healthy
	got, _ := r.step(2 * time.Second)
	if got != Render(r.st, PageNetwork, Layout{}) {
		t.Error("display stayed on the alerts page after the alerts cleared")
	}
}

func TestDirectorShowsNewAddress(t *testing.T) {
	r := newDirectorRig(testConfig())
	r.step(0)
	r.step(5 * time.Second) // now on the host page

	r.st.Net.Addr = "10.0.0.9"
	if got, _ := r.step(6 * time.Second); got != Render(r.st, PageNetwork, Layout{}) {
		t.Error("an address change did not bring up the network page")
	}
}

func TestDirectorDimsWhenIdleAndWakesOnEvents(t *testing.T) {
	cfg := testConfig() // dims after 10 minutes
	r := newDirectorRig(cfg)

	bright := brightness(cfg)
	if _, c := r.step(0); c != bright {
		t.Errorf("contrast at start = %#x, want %#x", c, bright)
	}
	if _, c := r.step(10*time.Minute - time.Second); c != bright {
		t.Errorf("dimmed early: %#x", c)
	}
	if _, c := r.step(10 * time.Minute); c != dimContrast {
		t.Errorf("contrast after idle = %#x, want %#x", c, dimContrast)
	}

	r.st.Net.Addr = "10.0.0.9"
	if _, c := r.step(11 * time.Minute); c != bright {
		t.Errorf("an event did not restore brightness: %#x", c)
	}
	if _, c := r.step(21 * time.Minute); c != dimContrast {
		t.Errorf("did not dim again after the event aged out: %#x", c)
	}

	// Something being wrong keeps the display bright for as long as it is.
	r.st = alertStatus()
	r.step(22 * time.Minute)
	if _, c := r.step(60 * time.Minute); c != bright {
		t.Errorf("dimmed with an alert showing: %#x", c)
	}
}

func TestDirectorNeverDimsWhenDisabledOrBrighterThanAsked(t *testing.T) {
	cfg := testConfig()
	cfg.DimAfterMinutes = 0
	r := newDirectorRig(cfg)
	r.step(0)
	if _, c := r.step(24 * time.Hour); c != brightness(cfg) {
		t.Errorf("dimAfterMinutes=0 still dimmed: %#x", c)
	}

	// "Dim" must never be brighter than the configured brightness.
	cfg = testConfig()
	cfg.Brightness = 0x02
	r = newDirectorRig(cfg)
	r.step(0)
	if _, c := r.step(time.Hour); c != 0x02 {
		t.Errorf("dim level %#x exceeds configured brightness 0x02", c)
	}
}

func TestDirectorOrbitsOnceAMinute(t *testing.T) {
	cfg := testConfig()
	cfg.PageSeconds = 3600 // hold one page so only the orbit moves
	cfg.DimAfterMinutes = 0
	r := newDirectorRig(cfg)

	base := Render(r.st, PageNetwork, Layout{})
	for i := range 2 * len(orbit) {
		after := time.Duration(i) * orbitEvery
		got, _ := r.step(after)
		if got != r.want(after, base) {
			t.Errorf("minute %d: frame is not at orbit step %v", i, orbit[i%len(orbit)])
		}
	}
}

func TestDirectorFollowsFlip(t *testing.T) {
	cfg := testConfig()
	cfg.Flip = true
	r := newDirectorRig(cfg)

	if got, _ := r.step(0); got != Render(r.st, PageNetwork, Layout{BandAtBottom: true}) {
		t.Error("a flipped panel did not move the header to the bottom band")
	}
}
