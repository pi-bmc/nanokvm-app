package ssd1306

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// recorder is a Bus that keeps every transfer, and can be told to fail.
type recorder struct {
	transfers [][]byte
	closed    bool
	// err, while set, fails every transfer with it.
	err error
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	r.transfers = append(r.transfers, bytes.Clone(p))
	return len(p), nil
}

func (r *recorder) Close() error {
	r.closed = true
	return nil
}

func (r *recorder) reset() { r.transfers = nil }

// commands returns every command byte sent, across transfers, in order.
func (r *recorder) commands() []byte {
	var out []byte
	for _, t := range r.transfers {
		if len(t) > 0 && t[0] == ctrlCommand {
			out = append(out, t[1:]...)
		}
	}
	return out
}

func TestInitProgramsPanelWithoutLightingIt(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{Contrast: 0x8F})

	if err := dev.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(bus.transfers) != 1 {
		t.Fatalf("Init used %d transfers, want 1", len(bus.transfers))
	}

	got := bus.commands()
	want := []byte{
		0xD5, 0x80,
		0xA8, 0x3F,
		0xD3, 0x00,
		0x40,
		0x8D, 0x14,
		0x20, 0x00,
		0xA1,
		0xC8,
		0xDA, 0x12,
		0x81, 0x8F,
		0xD9, 0xF1,
		0xDB, 0x40,
		0xA4,
		0xA6,
		0x2E,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Init commands\n got % X\nwant % X", got, want)
	}

	// Neither display-on nor display-off: lighting the panel is the
	// caller's decision (after the first frame), and re-running Init on a
	// lit panel must not blink it.
	for _, c := range got {
		if c == cmdDisplayOn || c == cmdDisplayOff {
			t.Errorf("Init sent 0x%02X; it must leave the panel's power state alone", c)
		}
	}
}

func TestFlipReversesBothAxes(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, Options{Flip: true}).Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got := bus.commands()
	if !bytes.Contains(got, []byte{cmdSegRemapOff, cmdComScanInc}) {
		t.Errorf("flipped Init did not select SEG remap off + COM scan inc: % X", got)
	}
	if bytes.Contains(got, []byte{cmdSegRemapOn}) || bytes.Contains(got, []byte{cmdComScanDec}) {
		// 0xA1 and 0xC8 do not occur as arguments in this sequence, so
		// their presence means the upright orientation leaked through.
		t.Errorf("flipped Init still carries the upright orientation: % X", got)
	}
}

func TestFirstDrawWritesWholeFrame(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	var buf [BufferSize]byte
	for i := range buf {
		buf[i] = byte(i)
	}
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	if len(bus.transfers) != 2 {
		t.Fatalf("first Draw used %d transfers, want 2 (window, data)", len(bus.transfers))
	}
	wantWindow := []byte{ctrlCommand, cmdColumnAddr, 0, Width - 1, cmdPageAddr, 0, Pages - 1}
	if !bytes.Equal(bus.transfers[0], wantWindow) {
		t.Errorf("window = % X, want % X", bus.transfers[0], wantWindow)
	}
	data := bus.transfers[1]
	if len(data) != 1+BufferSize || data[0] != ctrlData {
		t.Fatalf("data transfer is %d bytes starting 0x%02X, want %d starting 0x%02X",
			len(data), data[0], 1+BufferSize, ctrlData)
	}
	if !bytes.Equal(data[1:], buf[:]) {
		t.Error("data transfer does not carry the frame")
	}
}

func TestUnchangedFrameSendsNothing(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	var buf [BufferSize]byte
	buf[100] = 0xFF
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	bus.reset()

	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("second Draw: %v", err)
	}
	if len(bus.transfers) != 0 {
		t.Errorf("redrawing an identical frame used %d transfers, want 0", len(bus.transfers))
	}
}

func TestChangedColumnsAreSentAsOneSpanPerPage(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	var buf [BufferSize]byte
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	bus.reset()

	// Two changes on page 2 (columns 5 and 9) and one on page 6 (column 127).
	buf[2*Width+5] = 0x01
	buf[2*Width+9] = 0x80
	buf[6*Width+127] = 0x10
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	want := [][]byte{
		{ctrlCommand, cmdColumnAddr, 5, 9, cmdPageAddr, 2, 2},
		{ctrlData, 0x01, 0, 0, 0, 0x80},
		{ctrlCommand, cmdColumnAddr, 127, 127, cmdPageAddr, 6, 6},
		{ctrlData, 0x10},
	}
	if len(bus.transfers) != len(want) {
		t.Fatalf("got %d transfers, want %d: % X", len(bus.transfers), len(want), bus.transfers)
	}
	for i := range want {
		if !bytes.Equal(bus.transfers[i], want[i]) {
			t.Errorf("transfer %d = % X, want % X", i, bus.transfers[i], want[i])
		}
	}

	// The shadow must now match: another Draw is silent.
	bus.reset()
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if len(bus.transfers) != 0 {
		t.Errorf("shadow not updated: redraw used %d transfers", len(bus.transfers))
	}
}

// A failed transfer leaves the display RAM in an unknown state, so the next
// successful Draw has to repaint everything — even pixels the shadow thought
// were already right.
func TestFailedDrawForcesFullRepaint(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	var buf [BufferSize]byte
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	buf[0] = 0xFF
	bus.err = unix.EREMOTEIO
	if err := dev.Draw(&buf); err == nil {
		t.Fatal("Draw on a failing bus returned nil")
	}

	bus.err = nil
	bus.reset()
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw after recovery: %v", err)
	}
	if len(bus.transfers) != 2 || len(bus.transfers[1]) != 1+BufferSize {
		t.Errorf("recovery Draw was not a full repaint: %d transfers", len(bus.transfers))
	}
}

func TestInitInvalidatesShadow(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	var buf [BufferSize]byte
	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if err := dev.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	bus.reset()

	if err := dev.Draw(&buf); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if len(bus.transfers) != 2 {
		t.Errorf("Draw after re-Init used %d transfers, want a full repaint (2)", len(bus.transfers))
	}
}

func TestPowerAndContrast(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	if err := dev.SetPower(true); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetContrast(0x08); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetPower(false); err != nil {
		t.Fatal(err)
	}

	want := []byte{cmdDisplayOn, cmdSetContrast, 0x08, cmdDisplayOff}
	if got := bus.commands(); !bytes.Equal(got, want) {
		t.Errorf("commands = % X, want % X", got, want)
	}
}

// The service reprograms the panel every minute. If that went back to the
// construction-time contrast, a dimmed display would flash bright each time.
func TestInitKeepsTheLastContrast(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{Contrast: 0x8F})

	if err := dev.SetContrast(0x08); err != nil {
		t.Fatal(err)
	}
	bus.reset()
	if err := dev.Init(); err != nil {
		t.Fatal(err)
	}

	got := bus.commands()
	if !bytes.Contains(got, []byte{cmdSetContrast, 0x08}) {
		t.Errorf("re-Init did not program the current contrast: % X", got)
	}
	if bytes.Contains(got, []byte{cmdSetContrast, 0x8F}) {
		t.Errorf("re-Init went back to the original contrast: % X", got)
	}
}

func TestCloseBlanksThenReleases(t *testing.T) {
	bus := &recorder{}
	dev := New(bus, Options{})

	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := bus.commands(); !bytes.Equal(got, []byte{cmdDisplayOff}) {
		t.Errorf("Close sent % X, want display-off", got)
	}
	if !bus.closed {
		t.Error("Close did not release the bus")
	}
}

// An unplugged module cannot be switched off, and that must not stop the bus
// being released.
func TestCloseReleasesBusEvenWhenPanelIsGone(t *testing.T) {
	bus := &recorder{err: unix.EREMOTEIO}
	dev := New(bus, Options{})

	if err := dev.Close(); !IsAbsent(err) {
		t.Errorf("Close error = %v, want the panel's absence reported", err)
	}
	if !bus.closed {
		t.Error("Close did not release the bus")
	}
}

func TestIsAbsent(t *testing.T) {
	bus := &recorder{err: unix.EREMOTEIO}
	err := New(bus, Options{}).Init()
	if !IsAbsent(err) {
		t.Errorf("IsAbsent(%v) = false for an unacknowledged address", err)
	}
	if IsAbsent(errors.New("something else")) {
		t.Error("IsAbsent matched an unrelated error")
	}
	if IsAbsent(nil) {
		t.Error("IsAbsent(nil) = true")
	}
}

// The service tells "this kernel has no such bus" (stop for good) from
// "nothing answered" (keep probing) by fs.ErrNotExist surviving the wrap.
func TestOpenI2CMissingBusIsErrNotExist(t *testing.T) {
	_, err := OpenI2C(filepath.Join(t.TempDir(), "i2c-99"), DefaultAddr)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenI2C on a missing device = %v, want fs.ErrNotExist", err)
	}
}

func TestOpenI2CRejectsReservedAddresses(t *testing.T) {
	for _, addr := range []uint8{0x00, 0x07, 0x78, 0x7F, 0xFF} {
		if _, err := OpenI2C("/dev/null", addr); err == nil {
			t.Errorf("OpenI2C accepted reserved address 0x%02X", addr)
		}
	}
}
