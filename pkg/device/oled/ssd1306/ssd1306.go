// Package ssd1306 drives a 128x64 SSD1306 OLED controller over I2C.
//
// It is deliberately small: the panel is a status display that changes a few
// times a minute, so the driver's job is to put a framebuffer on the glass
// with as little bus traffic as it can get away with, and to make it cheap
// for the caller to recover when the module is unplugged, browns out or was
// never fitted. It knows nothing about what is being drawn — see the parent
// package for that.
//
// The controller is write-only in practice (its I2C read path returns one
// status byte and nothing of the display RAM), so everything here is
// open-loop: the only failure the driver can observe is a transfer the
// panel did not acknowledge.
package ssd1306

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Panel geometry. The controller's RAM is organised as 8 pages of 128
// column bytes; each byte is 8 vertically stacked pixels, LSB at the top.
const (
	Width  = 128
	Height = 64
	Pages  = Height / 8
	// BufferSize is one whole frame in the controller's own layout:
	// buf[page*Width+x], bit y%8.
	BufferSize = Width * Pages
)

// DefaultAddr is the 7-bit address these modules ship strapped to. The
// silkscreen says 0x78, which is the same address shifted left to make room
// for the R/W bit. The alternative strap is 0x3D (silkscreen 0x7A).
const DefaultAddr = 0x3C

// i2cSlave is I2C_SLAVE from linux/i2c-dev.h: bind this fd to a slave address
// so plain write() goes to that device.
const i2cSlave = 0x0703

// Control bytes. Every I2C transfer to the controller starts with one, and it
// says how to interpret the rest of the transfer: as commands, or as display
// RAM data.
const (
	ctrlCommand = 0x00
	ctrlData    = 0x40
)

// Commands used here. Only the fundamental set — none of the scrolling or
// fade features, which a status display has no use for.
const (
	cmdSetContrast      = 0x81 // + 1 byte
	cmdChargePump       = 0x8D // + 1 byte: 0x14 enable, 0x10 disable
	cmdMemoryMode       = 0x20 // + 1 byte: 0x00 horizontal
	cmdColumnAddr       = 0x21 // + start, end
	cmdPageAddr         = 0x22 // + start, end
	cmdStartLine0       = 0x40
	cmdSegRemapOff      = 0xA0 // column 0 -> SEG0
	cmdSegRemapOn       = 0xA1 // column 127 -> SEG0
	cmdResumeRAM        = 0xA4
	cmdNormalDisplay    = 0xA6
	cmdMultiplex        = 0xA8 // + 1 byte: rows - 1
	cmdDisplayOff       = 0xAE
	cmdDisplayOn        = 0xAF
	cmdComScanInc       = 0xC0
	cmdComScanDec       = 0xC8
	cmdDisplayOffset    = 0xD3 // + 1 byte
	cmdClockDiv         = 0xD5 // + 1 byte
	cmdPrecharge        = 0xD9 // + 1 byte
	cmdComPins          = 0xDA // + 1 byte
	cmdVcomDeselect     = 0xDB // + 1 byte
	cmdDeactivateScroll = 0x2E
)

// Bus is one I2C slave: each Write is a single START..STOP transfer to it.
//
// It is an interface so the driver can be tested against a recorder, and
// because that single-transfer property is all the driver depends on.
type Bus interface {
	io.Writer
	io.Closer
}

// OpenI2C binds to the slave at addr on a Linux i2c-dev bus such as
// "/dev/i2c-3".
//
// It does not talk to the device: a successful open says the bus exists, not
// that a panel is on it. Init is the first thing that finds out.
func OpenI2C(device string, addr uint8) (Bus, error) {
	if addr < 0x08 || addr > 0x77 {
		return nil, fmt.Errorf("ssd1306: 0x%02x is not a usable 7-bit i2c address", addr)
	}

	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("ssd1306: open %s: %w", device, err)
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), i2cSlave, uintptr(addr))
	if errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("ssd1306: bind %s to 0x%02x: %w", device, addr, errno)
	}

	return f, nil
}

// Options are the panel settings fixed for the life of a Dev.
type Options struct {
	// Flip rotates the image 180 degrees, for a module mounted with its pin
	// header at the bottom. It is done in the controller (segment remap and
	// COM scan direction), so it costs nothing per frame.
	Flip bool
	// Contrast is the initial segment current setting, 0..255.
	Contrast uint8
}

// Dev is one panel.
//
// It keeps a shadow of what it last wrote to the display RAM and sends only
// the columns that differ, so a clock ticking in one corner costs a handful
// of bytes on the wire rather than a kilobyte. Not safe for concurrent use;
// the display has exactly one writer.
type Dev struct {
	bus  Bus
	opts Options

	// shadow is what the display RAM holds, as far as this side knows. It
	// is only trusted while shadowValid is set: after a failed transfer, or
	// before the first full frame, the next Draw rewrites everything.
	shadow      [BufferSize]byte
	shadowValid bool

	// tx is the scratch transfer buffer: one control byte plus up to a
	// whole frame. Kept on the Dev so a redraw allocates nothing.
	tx [1 + BufferSize]byte
}

// New wraps bus. Nothing is written until Init.
func New(bus Bus, opts Options) *Dev {
	return &Dev{bus: bus, opts: opts}
}

// Init programs the panel configuration.
//
// It does not switch the display on and does not touch the display RAM, so
// the caller decides what the glass shows first: Init, Draw, then SetPower.
// A panel straight out of reset holds random RAM, and lighting it before
// the first frame shows that as a flash of noise.
//
// It is safe to call again on a running panel — every value is the one
// already in effect, so nothing visibly changes — which is how the caller
// repairs a module that lost power and came back in its reset state while
// still acknowledging its address. The shadow is dropped either way, since a
// reset panel's RAM is not what was last written to it.
func (d *Dev) Init() error {
	segRemap, comScan := byte(cmdSegRemapOn), byte(cmdComScanDec)
	if d.opts.Flip {
		segRemap, comScan = cmdSegRemapOff, cmdComScanInc
	}

	d.shadowValid = false
	return d.command(
		cmdClockDiv, 0x80, // oscillator default, divide ratio 1
		cmdMultiplex, Height-1,
		cmdDisplayOffset, 0x00,
		cmdStartLine0,
		cmdChargePump, 0x14, // the module has no external VCC; run the pump
		cmdMemoryMode, 0x00, // horizontal: the address pointer wraps within the window
		segRemap,
		comScan,
		cmdComPins, 0x12, // alternative COM layout, as wired on every 128x64 glass
		cmdSetContrast, d.opts.Contrast,
		cmdPrecharge, 0xF1,
		cmdVcomDeselect, 0x40,
		cmdResumeRAM,
		cmdNormalDisplay,
		cmdDeactivateScroll,
	)
}

// SetPower switches the panel on or off. Off is the controller's sleep mode:
// the charge pump stops and the glass goes dark, but the configuration and
// the display RAM are kept.
func (d *Dev) SetPower(on bool) error {
	if on {
		return d.command(cmdDisplayOn)
	}
	return d.command(cmdDisplayOff)
}

// SetContrast changes the segment current, 0..255. On these panels the
// visible range is narrow — 0 is dim, not dark — which is what makes it
// usable as an idle level without the display becoming unreadable.
//
// The level is remembered: a later Init programs it rather than the one the
// Dev was built with, so reprogramming a dimmed panel does not flash it
// bright for the length of a frame.
func (d *Dev) SetContrast(c uint8) error {
	if err := d.command(cmdSetContrast, c); err != nil {
		return err
	}
	d.opts.Contrast = c
	return nil
}

// Invalidate forgets what the display RAM holds, so the next Draw rewrites
// the whole frame.
func (d *Dev) Invalidate() { d.shadowValid = false }

// Draw makes the display RAM match buf, writing only what differs from the
// previous frame.
//
// On an error the shadow is dropped, because a transfer that failed part-way
// leaves the RAM in a state this side cannot know.
func (d *Dev) Draw(buf *[BufferSize]byte) error {
	if !d.shadowValid {
		if err := d.writeSpan(buf, 0, Pages-1, 0, Width-1); err != nil {
			return err
		}
		d.shadow = *buf
		d.shadowValid = true
		return nil
	}

	for page := range Pages {
		row := buf[page*Width : (page+1)*Width]
		old := d.shadow[page*Width : (page+1)*Width]

		first, last := -1, -1
		for x := range Width {
			if row[x] != old[x] {
				if first < 0 {
					first = x
				}
				last = x
			}
		}
		if first < 0 {
			continue
		}

		if err := d.writeSpan(buf, page, page, first, last); err != nil {
			d.shadowValid = false
			return err
		}
		copy(old[first:last+1], row[first:last+1])
	}
	return nil
}

// Close blanks the panel and releases the bus.
//
// The display is switched off rather than left showing its last frame: a
// status display that outlives the process drawing it would go on asserting
// a power state and an address nobody is maintaining any more. Failing to
// switch it off is not worth reporting over the close itself — the usual
// reason is that the module has already been unplugged.
func (d *Dev) Close() error {
	offErr := d.SetPower(false)
	closeErr := d.bus.Close()
	if closeErr != nil {
		return closeErr
	}
	return offErr
}

// writeSpan sets the address window to the given page and column ranges and
// fills it from buf. In horizontal mode the controller's pointer advances
// across the window and wraps to the next page at its right edge, so one data
// transfer covers the whole rectangle.
func (d *Dev) writeSpan(buf *[BufferSize]byte, page0, page1, col0, col1 int) error {
	// The casts are bounded by the panel geometry: pages 0..7, columns 0..127.
	if err := d.command(
		cmdColumnAddr, byte(col0), byte(col1), //nolint:gosec // G115: column index, 0..127
		cmdPageAddr, byte(page0), byte(page1), //nolint:gosec // G115: page index, 0..7
	); err != nil {
		return err
	}

	n := 1
	d.tx[0] = ctrlData
	for page := page0; page <= page1; page++ {
		n += copy(d.tx[n:], buf[page*Width+col0:page*Width+col1+1])
	}
	return d.transfer(d.tx[:n])
}

// command sends a run of command bytes in one transfer.
func (d *Dev) command(cmds ...byte) error {
	d.tx[0] = ctrlCommand
	n := 1 + copy(d.tx[1:], cmds)
	return d.transfer(d.tx[:n])
}

func (d *Dev) transfer(p []byte) error {
	n, err := d.bus.Write(p)
	if err != nil {
		return fmt.Errorf("ssd1306: write: %w", err)
	}
	if n != len(p) {
		return fmt.Errorf("ssd1306: %w: sent %d of %d bytes", io.ErrShortWrite, n, len(p))
	}
	return nil
}

// IsAbsent reports whether err is how an I2C master says nothing answered at
// the address — as opposed to the bus itself being missing or broken. The
// DesignWare controller on this board reports an unacknowledged address as
// EREMOTEIO; ENXIO is what most other Linux I2C drivers use for the same
// thing, and both mean "no panel here right now".
func IsAbsent(err error) bool {
	return errors.Is(err, unix.EREMOTEIO) || errors.Is(err, unix.ENXIO)
}
