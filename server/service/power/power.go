// Package power provides GPIO power management for the BMC.
//
// # Button-press mode (preferred)
//
// When the power-LED GPIO (GPIOPowerLED) is configured and readable, the
// controller operates in button-press mode:
//   - Power state is read from the LED pin (1 = on, 0 = off).
//   - The power-button GPIO (GPIOPower) is driven open-drain, matching a real
//     momentary button: the host holds it HIGH via an external pull-up, and a
//     press shorts it to ground. Releasing leaves the line high-impedance so the
//     host restores HIGH — the BMC never actively drives it high.
//   - A press is simulated by pulling the pin to ground (HIGH→LOW) for a
//     duration, then releasing (float back to HIGH).
//   - Short press (~300 ms): power-on or graceful/soft shutdown.
//   - Long press  (≥5 s):   forced hard shutdown (ATX-style hold).
//
// # Legacy / fallback mode
//
// When GPIOPowerLED is not configured or unreadable the controller falls back
// to direct GPIO control (original behaviour):
//   - State is read from the GPIOPower pin itself (1 = on, 0 = off).
//   - Power-on uses a 1→0→1 toggle sequence to trigger boot.
//   - Power-off / force-off set the pin directly to 0 (immediate cut).
//
// All public methods are serialized via a mutex.
package power

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/warthog618/go-gpiocdev"

	"github.com/BMCPi/NanoKVM/server/config"
	"github.com/BMCPi/NanoKVM/server/telemetry"
)

// gpioConsumer labels the process holding the GPIO lines (shown in gpioinfo).
const gpioConsumer = "nanokvm-power"

const (
	// shortPressDuration is the hold time for power-on and graceful shutdown.
	shortPressDuration = 300 * time.Millisecond

	// longPressDuration is the hold time for a forced power-off (ATX spec ≥4 s).
	longPressDuration = 5500 * time.Millisecond

	// toggleDelay is the pause between steps in the legacy boot sequence.
	toggleDelay = 200 * time.Millisecond

	// postPressDelay lets the hardware settle after releasing the button.
	postPressDelay = 500 * time.Millisecond

	// offPollInterval / offPollTimeout: how often / how long waitForOff polls.
	offPollInterval = 250 * time.Millisecond
	offPollTimeout  = 30 * time.Second
)

// Controller manages system power via GPIO. All public methods are serialized
// via a mutex to prevent concurrent GPIO sequences.
//
// GPIO lines are accessed through the character-device interface
// (CONFIG_GPIO_CDEV). Each line is requested once, lazily, and the request is
// held open for the process lifetime (lines map). Holding the request open is
// what keeps an output line driving its level after a call returns — closing it
// would release the line and drop the drive, which matters for legacy
// direct-drive power-off (pin must stay LOW). The kernel reclaims the lines when
// the process exits. Access to lines is serialized by mu together with the rest
// of the controller.
type Controller struct {
	mu    sync.Mutex
	lines map[config.GPIOPin]*gpiocdev.Line
}

var (
	instance *Controller
	once     sync.Once
)

// GetController returns the singleton power controller.
func GetController() *Controller {
	once.Do(func() {
		instance = &Controller{
			lines: make(map[config.GPIOPin]*gpiocdev.Line),
		}
	})
	return instance
}

// ── Public API ────────────────────────────────────────────────────────────────

// State reports whether the system is powered on.
// Reads the power-LED GPIO if available; otherwise reads the button pin directly.
func (c *Controller) State() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	on, err := c.readState()
	if err == nil {
		telemetry.PowerState(context.Background(), on)
	}
	return on, err
}

// PowerOn powers on the system.
//
// Button mode:  sends a short press if the system is currently off.
// Legacy mode:  runs the 1→0→1 boot sequence on the power pin.
func (c *Controller) PowerOn() (retErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { telemetry.PowerOperation(context.Background(), "on", retErr) }()

	on, err := c.readState()
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if on {
		log.Debug("power: already on, no-op")
		return nil
	}

	if !isLegacy() {
		log.Info("power: power-on (short button press)")
		return c.buttonPress(shortPressDuration)
	}

	log.Info("power: power-on boot sequence (legacy mode)")
	return c.legacyBootSequence()
}

// PowerOff sends a graceful/soft shutdown signal.
//
// Button mode:  short button press (OS handles the ACPI shutdown).
// Legacy mode:  sets the power pin to 0 (immediate cut).
func (c *Controller) PowerOff() (retErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { telemetry.PowerOperation(context.Background(), "off", retErr) }()

	on, err := c.readState()
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if !on {
		log.Debug("power: already off, no-op")
		return nil
	}

	if !isLegacy() {
		log.Info("power: soft shutdown (short button press)")
		return c.buttonPress(shortPressDuration)
	}

	log.Info("power: power-off (legacy — set pin 0)")
	return c.legacyWritePin(0)
}

// ForceOff forces an immediate hard shutdown.
//
// Button mode:  holds the power button LOW for ≥5 s.
// Legacy mode:  sets the power pin to 0 (same as PowerOff — immediate cut).
func (c *Controller) ForceOff() (retErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { telemetry.PowerOperation(context.Background(), "force_off", retErr) }()

	on, err := c.readState()
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if !on {
		log.Debug("power: already off, no-op")
		return nil
	}

	if !isLegacy() {
		log.Info("power: force shutdown (long button press ≥5 s)")
		return c.buttonPress(longPressDuration)
	}

	log.Info("power: force-off (legacy — set pin 0)")
	return c.legacyWritePin(0)
}

// Reset forces the system off and powers it back on.
func (c *Controller) Reset() (retErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { telemetry.PowerOperation(context.Background(), "reset", retErr) }()

	on, err := c.readState()
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}

	if on {
		if !isLegacy() {
			log.Info("power: reset — force off (long button press)")
			if err := c.buttonPress(longPressDuration); err != nil {
				return fmt.Errorf("reset force-off: %w", err)
			}
			if err := c.waitForOff(); err != nil {
				log.Warnf("power: reset — timed out waiting for off, proceeding anyway: %v", err)
			}
		} else {
			log.Info("power: reset — off (legacy)")
			if err := c.legacyWritePin(0); err != nil {
				return fmt.Errorf("reset legacy off: %w", err)
			}
			time.Sleep(toggleDelay)
		}
	}

	if !isLegacy() {
		log.Info("power: reset — power on (short press)")
		return c.buttonPress(shortPressDuration)
	}

	log.Info("power: reset — boot sequence (legacy)")
	return c.legacyBootSequence()
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// isLegacy reports whether legacy direct-GPIO mode is enabled via config.
func isLegacy() bool {
	return config.GetInstance().Power.LegacyMode
}

// readState returns true if the system is powered on.
// Button mode: reads the power-LED GPIO.
// Legacy mode:  reads the button/power pin directly.
// Caller must hold c.mu.
func (c *Controller) readState() (bool, error) {
	hw := config.GetInstance().Hardware

	if !isLegacy() {
		if hw.GPIOPowerLED.IsZero() {
			return false, fmt.Errorf("GPIOPowerLED not configured (required for button-press mode)")
		}
		// The LED pin is read-only input.
		v, err := c.readInput(hw.GPIOPowerLED)
		if err != nil {
			return false, fmt.Errorf("read power-LED pin: %w", err)
		}
		return v == 1, nil
	}

	// Legacy mode drives the power pin as output; reading it back reports the
	// level we last drove.
	v, err := c.readOutput(hw.GPIOPower)
	if err != nil {
		return false, fmt.Errorf("read power pin: %w", err)
	}
	return v == 1, nil
}

// buttonPress simulates a button press: ensures the pin is HIGH, pulls it LOW
// for duration, then restores HIGH. The pin is always left at 1 (released).
// Caller must hold c.mu.
func (c *Controller) buttonPress(duration time.Duration) error {
	pin := config.GetInstance().Hardware.GPIOPower
	if pin.IsZero() {
		return fmt.Errorf("GPIOPower not configured")
	}

	line, err := c.buttonLine(pin)
	if err != nil {
		return err
	}

	// Guarantee starting state is released (high-impedance, host holds HIGH).
	if err := line.SetValue(1); err != nil {
		return fmt.Errorf("pre-press release: %w", err)
	}

	// Pull to ground — button pressed (open-drain actively drives LOW).
	if err := line.SetValue(0); err != nil {
		return fmt.Errorf("press down: %w", err)
	}
	log.Debugf("power: gpio %s pressed (pulled to ground)", pin)
	time.Sleep(duration)

	// Release — float back to HIGH (open-drain high-impedance).
	if err := line.SetValue(1); err != nil {
		return fmt.Errorf("press release: %w", err)
	}
	log.Debugf("power: gpio %s released (floating high)", pin)

	time.Sleep(postPressDelay)
	return nil
}

// waitForOff polls until the power LED reports off, or until offPollTimeout.
// Caller must hold c.mu.
func (c *Controller) waitForOff() error {
	deadline := time.Now().Add(offPollTimeout)
	for time.Now().Before(deadline) {
		on, err := c.readState()
		if err == nil && !on {
			return nil
		}
		time.Sleep(offPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for power off", offPollTimeout)
}

// ── Legacy helpers ────────────────────────────────────────────────────────────

// legacyBootSequence performs the 1→0→1 toggle that triggers boot when the
// power pin is wired directly to the power supply enable.
// Caller must hold c.mu.
func (c *Controller) legacyBootSequence() error {
	pin := config.GetInstance().Hardware.GPIOPower
	for _, v := range []int{1, 0, 1} {
		if err := c.writeOutput(pin, v); err != nil {
			return fmt.Errorf("boot sequence (write %d): %w", v, err)
		}
		time.Sleep(toggleDelay)
	}
	return nil
}

// legacyWritePin sets the power pin directly (0 = off, 1 = on).
// Caller must hold c.mu.
func (c *Controller) legacyWritePin(val int) error {
	pin := config.GetInstance().Hardware.GPIOPower
	if err := c.writeOutput(pin, val); err != nil {
		return fmt.Errorf("write power pin %d: %w", val, err)
	}
	return nil
}

// ── Low-level GPIO access (character device, CONFIG_GPIO_CDEV) ─────────────────
//
// Lines are requested lazily and cached in c.lines; the request is held open so
// output levels persist after a call returns. All helpers require c.mu held,
// which the public API guarantees.

// outputLine returns a cached request for pin configured as output. On first
// use the line is sampled as input and then reconfigured to output at that same
// level, so acquiring the line does not glitch the current pin state (important
// so starting the daemon never toggles power).
func (c *Controller) outputLine(pin config.GPIOPin) (*gpiocdev.Line, error) {
	if l, ok := c.lines[pin]; ok {
		return l, nil
	}
	l, err := gpiocdev.RequestLine(pin.Chip, pin.Line, gpiocdev.AsInput, gpiocdev.WithConsumer(gpioConsumer))
	if err != nil {
		return nil, fmt.Errorf("request %s as input: %w", pin, err)
	}
	cur, err := l.Value()
	if err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("sample %s: %w", pin, err)
	}
	if err := l.Reconfigure(gpiocdev.AsOutput(cur)); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("reconfigure %s as output: %w", pin, err)
	}
	c.lines[pin] = l
	return l, nil
}

// buttonLine returns a cached open-drain request for the power-button pin.
//
// A momentary power button is active-low: the host holds it HIGH through an
// external pull-up, and a press shorts it to ground. Open-drain drive reproduces
// this exactly — value 0 pulls the line to ground (pressed); value 1 leaves it
// high-impedance (released) so the host pull-up restores HIGH. The BMC never
// actively sources HIGH, so it can't contend with the host's button circuit.
// gpiolib emulates open-drain on chips without native support, so this works
// regardless of the SoC. The initial value is released (high-impedance), which
// cannot disturb the host, so no glitch-free sampling is needed here.
func (c *Controller) buttonLine(pin config.GPIOPin) (*gpiocdev.Line, error) {
	if l, ok := c.lines[pin]; ok {
		return l, nil
	}
	l, err := gpiocdev.RequestLine(pin.Chip, pin.Line,
		gpiocdev.AsOutput(1),
		gpiocdev.AsOpenDrain,
		gpiocdev.WithConsumer(gpioConsumer))
	if err != nil {
		return nil, fmt.Errorf("request %s as open-drain output: %w", pin, err)
	}
	c.lines[pin] = l
	return l, nil
}

// inputLine returns a cached request for pin configured as input.
func (c *Controller) inputLine(pin config.GPIOPin) (*gpiocdev.Line, error) {
	if l, ok := c.lines[pin]; ok {
		return l, nil
	}
	l, err := gpiocdev.RequestLine(pin.Chip, pin.Line, gpiocdev.AsInput, gpiocdev.WithConsumer(gpioConsumer))
	if err != nil {
		return nil, fmt.Errorf("request %s as input: %w", pin, err)
	}
	c.lines[pin] = l
	return l, nil
}

// readInput reads the current level of an input pin (0 or 1).
func (c *Controller) readInput(pin config.GPIOPin) (int, error) {
	l, err := c.inputLine(pin)
	if err != nil {
		return 0, err
	}
	return l.Value()
}

// readOutput reads back the level currently driven on an output pin (0 or 1).
func (c *Controller) readOutput(pin config.GPIOPin) (int, error) {
	l, err := c.outputLine(pin)
	if err != nil {
		return 0, err
	}
	return l.Value()
}

// writeOutput drives an output pin to val (any non-zero value means high). The
// held-open line request keeps the level asserted after this returns.
func (c *Controller) writeOutput(pin config.GPIOPin, val int) error {
	l, err := c.outputLine(pin)
	if err != nil {
		return err
	}
	v := 0
	if val != 0 {
		v = 1
	}
	if err := l.SetValue(v); err != nil {
		return fmt.Errorf("set %s = %d: %w", pin, v, err)
	}
	log.Debugf("power: gpio %s = %d", pin, v)
	return nil
}
