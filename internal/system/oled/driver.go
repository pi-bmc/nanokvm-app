// Package oled provides a pure Go driver for the SSD1306 128x64 OLED display
// connected via I2C on NanoKVM hardware.
package oled

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	oledCmd  uint8 = 0x00
	oledData uint8 = 0x40

	addrAlphaBeta uint8 = 0x3D
	addrPcie      uint8 = 0x3C

	// I2C_SLAVE is the ioctl request code for setting the I2C slave address.
	I2C_SLAVE uintptr = 0x0703

	// HWAlpha is the default NanoKVM hardware version.
	HWAlpha = 0
	// HWBeta uses I2C bus 5 with address 0x3D.
	HWBeta = 1
	// HWPcie uses I2C bus 5 with address 0x3C and applies display offsets.
	HWPcie = 2
)

// OLED represents an SSD1306 OLED display connected via I2C.
type OLED struct {
	bus    *os.File
	addr   uint8
	hwVer  int
	exists bool
}

// New detects the hardware version, opens the I2C bus, and probes for OLED presence.
// The hardware version is read from /etc/kvm/hw:
//   - "alpha" or missing → bus 1, addr 0x3D
//   - "beta"             → bus 5, addr 0x3D
//   - "pcie"             → bus 5, addr 0x3C
func New() (*OLED, error) {
	o := &OLED{}

	// Detect hardware version from /etc/kvm/hw.
	if data, err := os.ReadFile("/etc/kvm/hw"); err == nil {
		content := strings.TrimSpace(string(data))
		if len(content) > 0 {
			switch content[0] {
			case 'b':
				o.hwVer = HWBeta
			case 'p':
				o.hwVer = HWPcie
			}
		}
	}

	// Determine I2C bus path and address.
	var busPath string
	switch o.hwVer {
	case HWAlpha:
		busPath = "/dev/i2c-1"
		o.addr = addrAlphaBeta
	case HWBeta:
		busPath = "/dev/i2c-5"
		o.addr = addrAlphaBeta
	case HWPcie:
		busPath = "/dev/i2c-5"
		o.addr = addrPcie
	}

	// Open I2C bus.
	var err error
	o.bus, err = os.OpenFile(busPath, os.O_RDWR, 0)
	if err != nil {
		return o, fmt.Errorf("open i2c bus %s: %w", busPath, err)
	}

	// Set I2C slave address.
	if err := o.setAddress(); err != nil {
		o.bus.Close()
		return o, fmt.Errorf("set i2c address 0x%02X: %w", o.addr, err)
	}

	// Probe OLED by writing display-off command directly (bypasses exists guard).
	if _, err := o.bus.Write([]byte{oledCmd, 0xAE}); err == nil {
		o.exists = true
	}

	return o, nil
}

// Close releases the I2C bus file descriptor.
func (o *OLED) Close() {
	if o.bus != nil {
		o.bus.Close()
	}
}

// Exists returns whether an OLED display was detected on the I2C bus.
func (o *OLED) Exists() bool {
	return o.exists
}

// HWVersion returns the detected hardware version (HWAlpha, HWBeta, or HWPcie).
func (o *OLED) HWVersion() int {
	return o.hwVer
}

func (o *OLED) setAddress() error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, o.bus.Fd(), I2C_SLAVE, uintptr(o.addr))
	if errno != 0 {
		return errno
	}
	return nil
}

func (o *OLED) writeRegister(mode, data uint8) error {
	if !o.exists {
		return nil
	}
	_, err := o.bus.Write([]byte{mode, data})
	return err
}

// Init performs the standard SSD1306 initialization sequence.
func (o *OLED) Init() {
	cmds := []uint8{
		0xAE,       // Display off
		0x00,       // Set low column address
		0x10,       // Set high column address
		0x40,       // Set start line address
		0x81, 0xCF, // Set contrast
		0xA1,       // Set segment re-map
		0xC8,       // Set COM output scan direction
		0xA6,       // Set normal display
		0xA8, 0x3F, // Set multiplex ratio (1 to 64)
		0xD3, 0x00, // Set display offset (no offset)
		0xD5, 0x80, // Set display clock divide ratio
		0xD9, 0xF1, // Set pre-charge period
		0xDA, 0x12, // Set COM pins hardware configuration
		0xDB, 0x40, // Set VCOMH deselect level
		0x20, 0x02, // Set page addressing mode
		0x8D, 0x14, // Enable charge pump
		0xA4,       // Display from RAM
		0xA6,       // Normal display
	}
	for _, cmd := range cmds {
		o.writeRegister(oledCmd, cmd)
	}
	o.Clear()
	o.writeRegister(oledCmd, 0xAF) // Display on
}

// Clear sets all pixels to off (black screen).
func (o *OLED) Clear() {
	for i := uint8(0); i < 8; i++ {
		o.writeRegister(oledCmd, 0xB0+i)
		o.writeRegister(oledCmd, 0x00)
		o.writeRegister(oledCmd, 0x10)
		for n := 0; n < 128; n++ {
			o.writeRegister(oledData, 0x00)
		}
	}
}

// Fill sets all pixels to on (white screen).
func (o *OLED) Fill() {
	for i := uint8(0); i < 8; i++ {
		o.writeRegister(oledCmd, 0xB0+i)
		o.writeRegister(oledCmd, 0x00)
		o.writeRegister(oledCmd, 0x10)
		for n := 0; n < 128; n++ {
			o.writeRegister(oledData, 0xFF)
		}
	}
}

// SetPos sets the OLED cursor position in page addressing mode.
// For PCIe hardware, x is offset by +32 and y by +8.
func (o *OLED) SetPos(x, y uint8) {
	if o.hwVer == HWPcie {
		x += 32
		y += 8
	}
	o.writeRegister(oledCmd, 0xB0+y)
	o.writeRegister(oledCmd, ((x&0xF0)>>4)|0x10)
	o.writeRegister(oledCmd, x&0x0F)
}

// ShowChar renders a single character at position (x, y) using the specified font size.
// Supported sizes: 4 (4x8), 8 (6x8), 16 (8x16).
func (o *OLED) ShowChar(x, y uint8, chr byte, sizey uint8) {
	c := int(chr - ' ')
	sizex := uint16(sizey / 2)
	var size1 uint16

	switch sizey {
	case 8:
		size1 = 6
	case 4:
		size1 = 4
	default:
		extra := uint16(0)
		if sizey%8 != 0 {
			extra = 1
		}
		size1 = (uint16(sizey)/8 + extra) * uint16(sizey/2)
	}

	o.SetPos(x, y)
	for i := uint16(0); i < size1; i++ {
		if sizex > 0 && i%sizex == 0 && sizey == 16 {
			o.SetPos(x, y)
			y++
		}
		switch sizey {
		case 8:
			if c >= 0 && c < len(Asc2_0806) {
				o.writeRegister(oledData, Asc2_0806[c][i])
			}
		case 16:
			if c >= 0 && c < len(Asc2_1608) {
				o.writeRegister(oledData, Asc2_1608[c][i])
			}
		case 4:
			if c >= 0 && c < len(Asc2_0804) {
				o.writeRegister(oledData, Asc2_0804[c][i])
			}
		default:
			return
		}
	}
}

// showCharTurn renders an inverted (negative) character at position (x, y).
func (o *OLED) showCharTurn(x, y uint8, chr byte, sizey uint8) {
	c := int(chr - ' ')
	sizex := uint16(sizey / 2)
	var size1 uint16

	switch sizey {
	case 8:
		size1 = 6
	default:
		extra := uint16(0)
		if sizey%8 != 0 {
			extra = 1
		}
		size1 = (uint16(sizey)/8 + extra) * uint16(sizey/2)
	}

	o.SetPos(x, y)
	for i := uint16(0); i < size1; i++ {
		if sizex > 0 && i%sizex == 0 && sizey != 8 {
			o.SetPos(x, y)
			y++
		}
		switch sizey {
		case 8:
			if c >= 0 && c < len(Asc2_0806) {
				o.writeRegister(oledData, ^Asc2_0806[c][i])
			}
		case 16:
			if c >= 0 && c < len(Asc2_1608) {
				o.writeRegister(oledData, ^Asc2_1608[c][i])
			}
		default:
			return
		}
	}
}

// ShowString renders a string at position (x, y) with the given font size.
func (o *OLED) ShowString(x, y uint8, str string, sizey uint8) {
	for j := 0; j < len(str); j++ {
		o.ShowChar(x, y, str[j], sizey)
		switch sizey {
		case 8:
			x += 6
		case 4:
			x += 4
		default:
			x += sizey / 2
		}
	}
}

// ShowStringTurn renders an inverted (negative) string at position (x, y).
func (o *OLED) ShowStringTurn(x, y uint8, str string, sizey uint8) {
	for j := 0; j < len(str); j++ {
		o.showCharTurn(x, y, str[j], sizey)
		if sizey == 8 {
			x += 6
		} else {
			x += sizey / 2
		}
	}
}

// ShowStringAlignRight renders a right-aligned string ending at xEnd.
func (o *OLED) ShowStringAlignRight(xEnd, y uint8, str string, size uint8) {
	x := xEnd
	j := len(str)
	for j > 0 {
		if size == 8 {
			x -= 6
		} else {
			x -= 4
		}
		o.ShowChar(x, y, str[j-1], size)
		j--
	}
}

// ShowStringToEnd renders a string at position (x, y) until the delimiter byte.
func (o *OLED) ShowStringToEnd(x, y uint8, str string, sizey uint8, end byte) {
	for j := 0; j < len(str); j++ {
		if str[j] == end {
			break
		}
		o.ShowChar(x, y, str[j], sizey)
		if sizey == 8 {
			x += 6
		} else {
			x += sizey / 2
		}
	}
}

// oledPow calculates m^n for small integer values used in digit extraction.
func oledPow(m, n uint8) uint32 {
	result := uint32(1)
	for i := uint8(0); i < n; i++ {
		result *= uint32(m)
	}
	return result
}

// ShowNum renders a number at position (x, y) with leading space suppression.
func (o *OLED) ShowNum(x, y, num, length, sizey uint8) {
	var m uint8
	if sizey == 8 {
		m = 2
	}
	enshow := false
	for t := uint8(0); t < length; t++ {
		temp := uint8((uint32(num) / oledPow(10, length-t-1)) % 10)
		if !enshow && t < length-1 {
			if temp == 0 {
				o.ShowChar(x+(sizey/2+m)*t, y, ' ', sizey)
				continue
			}
			enshow = true
		}
		o.ShowChar(x+(sizey/2+m)*t, y, temp+'0', sizey)
	}
}

// ShowState renders a state icon from the KVM icon table at position (x, y).
func (o *OLED) ShowState(x, y, chr, size uint8) {
	if int(chr) >= len(KvmIcons) {
		return
	}
	o.SetPos(x, y)
	for i := uint8(0); i < size && int(i) < len(KvmIcons[chr]); i++ {
		o.writeRegister(oledData, KvmIcons[chr][i])
	}
}

// ShowKVMLogo renders the NanoKVM logo on the display.
func (o *OLED) ShowKVMLogo() {
	x := uint8(20)
	var y uint8
	for i := 0; i < len(KvmLogo); i++ {
		if i%2 == 0 {
			x++
			y = 0
		} else {
			y = 1
		}
		o.SetPos(x, y)
		o.writeRegister(oledData, KvmLogo[i])
	}
}

// ShowLogo renders the Sipeed logo, or a custom logo from /boot/logo.bin
// if the file exists and is exactly 32 bytes.
func (o *OLED) ShowLogo() {
	var logoData [32]byte
	useFile := false
	if data, err := os.ReadFile("/boot/logo.bin"); err == nil && len(data) == 32 {
		copy(logoData[:], data)
		useFile = true
	}

	x := uint8(0)
	var y uint8
	for i := 0; i < 32; i++ {
		if i%2 == 0 {
			x++
			y = 0
		} else {
			y = 1
		}
		o.SetPos(x, y)
		if useFile {
			o.writeRegister(oledData, logoData[i])
		} else {
			o.writeRegister(oledData, SipeedLogo[i])
		}
	}
}

// Showline renders vertical separator lines on the display.
func (o *OLED) Showline() {
	x := uint8(3)
	for y := uint8(2); y < 8; y++ {
		o.SetPos(x, y)
		if y == 2 {
			o.writeRegister(oledData, 0xC0)
			o.writeRegister(oledData, 0xC0)
			o.writeRegister(oledData, 0xC0)
		} else {
			o.writeRegister(oledData, 0xFF)
			o.writeRegister(oledData, 0xFF)
			o.writeRegister(oledData, 0xFF)
		}
	}
}

// Showline1 renders a short separator using the last 4 bytes of the KVM logo.
func (o *OLED) Showline1() {
	x := uint8(18)
	var y uint8
	for i := 112; i < 116 && i < len(KvmLogo); i++ {
		if i%2 == 0 {
			x++
			y = 0
		} else {
			y = 1
		}
		o.SetPos(x, y)
		o.writeRegister(oledData, KvmLogo[i])
	}
}

// ColorTurn sets the display to normal (0) or inverted (1) mode.
func (o *OLED) ColorTurn(i uint8) {
	if i == 0 {
		o.writeRegister(oledCmd, 0xA6)
	} else if i == 1 {
		o.writeRegister(oledCmd, 0xA7)
	}
}

// DisplayTurn sets the display orientation: 0 for normal, 1 for 180° rotation.
func (o *OLED) DisplayTurn(i uint8) {
	if i == 0 {
		o.writeRegister(oledCmd, 0xC8)
		o.writeRegister(oledCmd, 0xA1)
	} else if i == 1 {
		o.writeRegister(oledCmd, 0xC0)
		o.writeRegister(oledCmd, 0xA0)
	}
}

// Revolve rotates the display 180 degrees.
func (o *OLED) Revolve() {
	o.writeRegister(oledCmd, 0xA0)
	o.writeRegister(oledCmd, 0xC0)
}

// DisplayOn turns the OLED display on with charge pump enabled.
func (o *OLED) DisplayOn() {
	o.writeRegister(oledCmd, 0x8D)
	o.writeRegister(oledCmd, 0x14)
	o.writeRegister(oledCmd, 0xAF)
}

// DisplayOff turns the OLED display off with charge pump disabled.
func (o *OLED) DisplayOff() {
	o.writeRegister(oledCmd, 0x8D)
	o.writeRegister(oledCmd, 0x10)
	o.writeRegister(oledCmd, 0xAE)
}

// ShowIMG renders raw image data at position (x, y) with given dimensions.
// Height is in pages (8-pixel rows).
func (o *OLED) ShowIMG(x, y uint8, data []byte, width, height uint8) {
	size := int(width) * int(height)
	for count := 0; count < size && count < len(data); count++ {
		if int(width) > 0 && count%int(width) == 0 {
			o.SetPos(x, y+uint8(count/int(width)))
		}
		o.writeRegister(oledData, data[count])
	}
}

// ShowError renders or clears an error icon at position (x, y).
// When state is non-zero, the [!] icon is drawn; when zero, the area is cleared.
func (o *OLED) ShowError(x, y, state uint8) {
	if state != 0 {
		o.SetPos(x, y)
		for i := 0; i < len(EthError); i++ {
			o.writeRegister(oledData, EthError[i])
		}
	} else {
		o.ShowString(28, 3, " ", 8)
	}
}

// Row enables (non-zero) or disables (zero) horizontal scrolling.
func (o *OLED) Row(en uint8) {
	if en != 0 {
		o.writeRegister(oledCmd, 0x2E) // Stop scrolling
		o.writeRegister(oledCmd, 0x27) // Scroll left
		o.writeRegister(oledCmd, 0x00) // Dummy byte
		o.writeRegister(oledCmd, 0x00) // Start page 0
		o.writeRegister(oledCmd, 0x07) // Scroll interval
		o.writeRegister(oledCmd, 0x07) // End page 7
		o.writeRegister(oledCmd, 0x0F) // Dummy byte
		o.writeRegister(oledCmd, 0x7F) // Dummy byte
		o.writeRegister(oledCmd, 0x2F) // Start scrolling
	} else {
		o.writeRegister(oledCmd, 0x2E) // Stop scrolling
	}
}
