package oled

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pi-bmc/nanokvm-app/pkg/config"
	"github.com/pi-bmc/nanokvm-app/pkg/device/oled/ssd1306"
	"github.com/pi-bmc/nanokvm-app/pkg/logger"
)

// Timing. None of it is configurable: these are properties of how the
// display behaves, not preferences.
const (
	// tickEvery is how often the status is sampled and the frame reconsidered.
	// It bounds how stale the display can be — a power transition shows
	// within a second — and costs almost nothing while nothing changes,
	// since an unchanged frame puts no bytes on the bus.
	tickEvery = time.Second
	// probeEvery is how often an absent panel is looked for again. A
	// module on a pin header gets plugged in after boot; finding it within
	// a few seconds is the difference between hot-plug working and "you
	// have to restart the BMC".
	probeEvery = 10 * time.Second
	// reassertEvery is how often a panel that is answering is reprogrammed
	// and repainted from scratch anyway. The controller cannot be read
	// back, so a module that browned out and reset — display off, RAM
	// random — still acknowledges every write and looks healthy from here.
	// Reasserting is the only repair available, and it is invisible on a
	// panel that did not need it.
	reassertEvery = time.Minute
	// splashFor is how long a power transition holds the body area.
	splashFor = 3 * time.Second
	// orbitEvery is how often the image moves one step round the orbit.
	orbitEvery = time.Minute
)

// dimContrast is the contrast an idle display drops to. Not zero: the point
// is to slow the wear on a panel that is lit for years, and it has no button
// to wake it, so it must stay readable.
const dimContrast = 0x08

// orbit is the path the whole image walks, one step per orbitEvery, so that
// a display showing the same hostname for years does not burn it in as
// sharply. One pixel is enough to blur an edge and too little to notice;
// every layout keeps a dark row and column free for it.
var orbit = [...]image.Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}

// Controller is the SoC I2C controller the display is wired to, by the name
// the kernel gives its platform device: IIC3, at MMIO 0x04030000.
//
// The bus is found by this name rather than assumed to be /dev/i2c-3, for the
// reason pkg/config resolves GPIO chips by label: an adapter number is
// assigned in registration order and a device-tree alias, so it identifies
// nothing by itself. On this board a renumbering would point the display at
// the HDMI bridge's bus; on any other machine this binary is run on, "bus 3"
// is whatever happens to be third — a workstation's SMBus, say — and the
// first thing the service does to a bus is write to it. A controller address
// is fixed by the SoC, so either the right bus is found or none is.
const Controller = "4030000.i2c"

// platformDevices is where sysfs lists platform devices by name.
const platformDevices = "/sys/bus/platform/devices"

// FindBus returns the i2c-dev path of the adapter belonging to the named
// platform controller, looking under root (platformDevices on a real system).
// A controller that is absent, disabled in the device tree, or has no adapter
// registered is reported as fs.ErrNotExist.
func FindBus(root, controller string) (string, error) {
	// An I2C adapter is a child of its controller's device, named i2c-N.
	matches, err := filepath.Glob(filepath.Join(root, controller, "i2c-[0-9]*"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no i2c adapter on controller %s: %w", controller, fs.ErrNotExist)
	}
	return "/dev/" + filepath.Base(matches[0]), nil
}

// StatusFunc samples what the display should show. It is called once a tick
// from the service's own goroutine and should not block for long: while it
// does, the display is not updating.
type StatusFunc func(ctx context.Context) Status

// Service owns the panel: it finds it, keeps it painted, survives it being
// unplugged, and blanks it on the way out.
type Service struct {
	cfg    config.Display
	addr   uint8
	status StatusFunc
	log    *slog.Logger

	// findBus locates the default bus when the config names none; replaced
	// in tests.
	findBus func() (string, error)
	// open is how the bus is reached; replaced in tests.
	open func(device string, addr uint8) (ssd1306.Bus, error)
	// now is the clock; replaced in tests.
	now func() time.Time
	// tick is the loop period; shortened in tests.
	tick time.Duration

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New builds a Service from the display config section. Nothing happens
// until Start.
func New(cfg config.Display, status StatusFunc, log *slog.Logger) *Service {
	return &Service{
		cfg:     cfg,
		status:  status,
		log:     logger.Or(log),
		findBus: func() (string, error) { return FindBus(platformDevices, Controller) },
		open:    ssd1306.OpenI2C,
		now:     time.Now,
		tick:    tickEvery,
	}
}

// Start begins driving the display in the background. It returns at once and
// reports nothing about the panel: a bus with no module on it is an ordinary
// state that may change, not a startup failure. It is a no-op when the
// display is disabled, and when called twice.
//
// An unusable address is the one thing it refuses, because that is a typo in
// the config rather than a state of the hardware, and probing some other
// address in its place would be guessing.
func (s *Service) Start(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}

	addr, err := ParseAddress(s.cfg.Address)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.started = true
	s.addr = addr

	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx)
	return nil
}

// Stop blanks the display and waits for the service goroutine to finish.
// Safe to call on a service that was never started.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// ParseAddress reads a 7-bit I2C address as the config file spells it:
// "0x3c", or plain decimal.
func ParseAddress(s string) (uint8, error) {
	v, err := strconv.ParseUint(s, 0, 8)
	if err != nil {
		return 0, fmt.Errorf("display.address %q: not a number: %w", s, err)
	}
	// 0x00-0x07 and 0x78-0x7F are reserved by the I2C specification.
	if v < 0x08 || v > 0x77 {
		return 0, fmt.Errorf("display.address %q: outside the usable 7-bit range 0x08-0x77", s)
	}
	return uint8(v), nil
}

// brightness is the configured contrast as the controller takes it. The
// config carries an int so that a typo cannot fail the whole file's parse;
// pkg/config already keeps it in range, and this holds the line for a
// hand-built Display that did not go through it.
func brightness(cfg config.Display) uint8 {
	return uint8(min(max(cfg.Brightness, 1), 0xFF))
}

// panelState is what the service last knew about the panel, kept only so
// that each change is logged once instead of once per probe.
type panelState int

const (
	panelUnknown panelState = iota
	panelOnline
	panelAbsent
	panelFault
)

// run is the service loop: bring the panel up when it is there, repaint it
// when the picture changes, and go back to probing when it stops answering.
func (s *Service) run(ctx context.Context) {
	defer close(s.done)

	p := &panel{svc: s, dir: newDirector(s.cfg, s.now())}
	defer p.release()

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	for {
		if !p.tick(ctx, s.now()) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// panel is the service loop's view of the hardware across ticks.
type panel struct {
	svc *Service
	dir *director

	// dev is nil while there is no panel to draw on.
	dev   *ssd1306.Dev
	state panelState

	// probeAt and reassertAt are when each is next due.
	probeAt    time.Time
	reassertAt time.Time

	// contrast is what the panel is currently set to; lit is whether it
	// has been switched on since it was last programmed.
	contrast uint8
	lit      bool
}

// tick does one iteration's work. It returns false when the display can
// never come up and the loop should end.
func (p *panel) tick(ctx context.Context, now time.Time) bool {
	s := p.svc

	if p.dev == nil {
		if now.Before(p.probeAt) {
			return true
		}

		dev, err := s.bringUp()
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// No such bus. That is the device tree, and it will not
			// change while this kernel is running, so there is
			// nothing to wait for.
			s.log.Info("display: i2c bus not present on this board; display disabled",
				slog.String("device", s.deviceLabel()))
			return false
		case err != nil:
			p.probeAt = now.Add(probeEvery)
			s.note(&p.state, err)
			return true
		}
		p.dev = dev
		p.contrast = brightness(s.cfg)
		p.programmed(now)
	}

	if err := p.paint(ctx, now); err != nil {
		p.release()
		p.probeAt = now.Add(probeEvery)
		s.note(&p.state, err)
		return true
	}
	s.note(&p.state, nil)
	return true
}

// programmed records that the panel has just been (re)initialised: it has to
// be treated as switched off with unknown RAM. Its contrast is whatever it
// was last set to — the driver reprograms that, not the configured level, so
// a dimmed panel stays dim through a reassert.
func (p *panel) programmed(now time.Time) {
	p.lit = false
	p.reassertAt = now.Add(reassertEvery)
}

// paint puts this tick's frame on the glass at the contrast the director
// wants. The frame goes first and the panel is switched on after it, so a
// freshly programmed panel lights up already showing the picture rather than
// whatever was in its RAM.
func (p *panel) paint(ctx context.Context, now time.Time) error {
	if !now.Before(p.reassertAt) {
		if err := p.dev.Init(); err != nil {
			return err
		}
		p.programmed(now)
	}

	frame, want := p.dir.step(now, p.svc.status(ctx))
	if err := p.dev.Draw((*[ssd1306.BufferSize]byte)(&frame)); err != nil {
		return err
	}
	if want != p.contrast {
		if err := p.dev.SetContrast(want); err != nil {
			return err
		}
		p.contrast = want
	}
	if !p.lit {
		if err := p.dev.SetPower(true); err != nil {
			return err
		}
		p.lit = true
	}
	return nil
}

// release blanks the panel and lets go of the bus. The error is dropped: the
// usual reason to be here is that the panel has stopped answering.
func (p *panel) release() {
	if p.dev != nil {
		_ = p.dev.Close()
		p.dev = nil
	}
}

// bringUp opens the bus and programs the panel. Programming is the probe:
// it is the first transfer, so an empty connector fails here.
func (s *Service) bringUp() (*ssd1306.Dev, error) {
	device, err := s.device()
	if err != nil {
		return nil, err
	}

	bus, err := s.open(device, s.addr)
	if err != nil {
		return nil, err
	}

	dev := ssd1306.New(bus, ssd1306.Options{Flip: s.cfg.Flip, Contrast: brightness(s.cfg)})
	if err := dev.Init(); err != nil {
		_ = bus.Close()
		return nil, err
	}
	return dev, nil
}

// device is the i2c-dev path to open: the configured one, or else wherever
// the kernel put the board's display controller.
func (s *Service) device() (string, error) {
	if s.cfg.Device != "" {
		return s.cfg.Device, nil
	}
	return s.findBus()
}

// deviceLabel names the bus for a log line, including when it could not be
// resolved to a path.
func (s *Service) deviceLabel() string {
	if device, err := s.device(); err == nil {
		return device
	}
	return "controller " + Controller
}

// note records the panel's state and logs it when it changes. err is nil for
// a panel that is painting.
func (s *Service) note(state *panelState, err error) {
	next := panelOnline
	switch {
	case err == nil:
	case ssd1306.IsAbsent(err):
		next = panelAbsent
	default:
		next = panelFault
	}
	if next == *state {
		return
	}
	prev := *state
	*state = next

	attrs := []any{
		slog.String("device", s.deviceLabel()),
		slog.String("address", fmt.Sprintf("0x%02x", s.addr)),
	}
	switch next {
	case panelOnline:
		s.log.Info("display: panel online", attrs...)
	case panelAbsent:
		// Nothing answering is the normal state of a board with a bare
		// header, so it is only worth a warning when a panel that was
		// working has gone away.
		if prev == panelOnline {
			s.log.Warn("display: panel stopped responding; will keep probing", attrs...)
		} else {
			s.log.Info("display: no panel answered; will keep probing", attrs...)
		}
	case panelFault:
		s.log.Warn("display: i2c bus error; will keep retrying", append(attrs, slog.Any("err", err))...)
	case panelUnknown:
	}
}

// director decides what the display shows from one tick to the next: which
// page is up, whether a transition has taken over the body, where the orbit
// has got to, and how bright the panel should be. It holds no reference to
// the panel and takes the time as an argument, so the whole policy can be
// stepped through in a test.
type director struct {
	lay       Layout
	pageEvery time.Duration
	dimAfter  time.Duration
	bright    uint8

	start time.Time

	primed bool
	// page is the page currently up, and pageSince when it came up.
	page      Page
	pageSince time.Time

	// The last status's facts that count as events when they change.
	power  PowerState
	addr   string
	alerts int

	// splash is the message holding the body until splashUntil.
	splash      string
	splashUntil time.Time

	// lastEvent is when something last happened that is worth full
	// brightness.
	lastEvent time.Time
}

func newDirector(cfg config.Display, now time.Time) *director {
	return &director{
		lay:       Layout{BandAtBottom: cfg.Flip},
		pageEvery: time.Duration(cfg.PageSeconds) * time.Second,
		dimAfter:  time.Duration(cfg.DimAfterMinutes) * time.Minute,
		bright:    brightness(cfg),
		start:     now,
	}
}

// step returns the frame for this instant and the contrast to show it at.
func (d *director) step(now time.Time, st Status) (Frame, uint8) {
	pages := Pages(st)
	alerts := len(st.alerts())

	if !d.primed {
		d.primed = true
		d.page, d.pageSince = pages[0], now
		d.lastEvent = now
	} else {
		d.react(now, st, alerts)
	}
	d.power, d.addr, d.alerts = st.Host.Power, st.Net.Addr, alerts

	d.advance(now, pages)

	var frame Frame
	if now.Before(d.splashUntil) {
		frame = RenderSplash(st, d.splash, d.lay)
	} else {
		frame = Render(st, d.page, d.lay)
	}

	o := orbit[int(now.Sub(d.start)/orbitEvery)%len(orbit)]
	return frame.shift(o.X, o.Y), d.contrast(now, alerts)
}

// react turns a change since the last tick into what the display does about
// it: wake to full brightness, and bring up the page the change is on.
func (d *director) react(now time.Time, st Status, alerts int) {
	power := st.Host.Power
	switch {
	// A transition into or out of "unknown" is the sense line appearing
	// or failing, not the host changing state; it gets no fanfare.
	case power != d.power && power != PowerUnknown && d.power != PowerUnknown:
		d.splash = "POWER OFF"
		if power == PowerOn {
			d.splash = "POWER ON"
		}
		d.splashUntil = now.Add(splashFor)
		// When the splash clears, the host page is the one that says
		// what the machine is doing about having been switched.
		d.show(now.Add(splashFor), PageHost)
		d.lastEvent = now

	case alerts > d.alerts:
		d.show(now, PageAlerts)
		d.lastEvent = now

	case st.Net.Addr != d.addr:
		d.show(now, PageNetwork)
		d.lastEvent = now
	}
}

// show brings page up, as of at.
func (d *director) show(at time.Time, page Page) {
	d.page, d.pageSince = page, at
}

// advance moves to the next page once the current one has had its time, and
// off a page that has left the rotation (the alerts page, once clear).
func (d *director) advance(now time.Time, pages []Page) {
	idx := -1
	for i, p := range pages {
		if p == d.page {
			idx = i
			break
		}
	}

	switch {
	case idx < 0:
		d.show(now, pages[0])
	case now.Sub(d.pageSince) >= d.pageEvery:
		d.show(now, pages[(idx+1)%len(pages)])
	}
}

// contrast is full brightness while something is wrong or has recently
// changed, and the dim level once the display has had nothing new to say for
// dimAfter. Zero disables dimming.
func (d *director) contrast(now time.Time, alerts int) uint8 {
	if d.dimAfter <= 0 || alerts > 0 || now.Sub(d.lastEvent) < d.dimAfter {
		return d.bright
	}
	return min(d.bright, dimContrast)
}
