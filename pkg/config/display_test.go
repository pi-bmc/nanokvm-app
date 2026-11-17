package config

// display_test.go covers the display section's defaulting. The display is
// default-on optional hardware, so the cases that matter are the upgrade (a
// config that predates the section must start driving a panel) and every way
// an operator can say "no" or "not like that" and have it survive a load.

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDisplayAbsentSectionGetsDefaults is the upgrade case: no display: block
// at all, as in every config written before the feature existed.
func TestDisplayAbsentSectionGetsDefaults(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret)

	if c.Display != defaultConfig.Display {
		t.Errorf("Display = %+v, want the defaults %+v", c.Display, defaultConfig.Display)
	}
	if !c.Display.Enabled {
		t.Error("Display.Enabled = false for a config with no display section, want true")
	}
}

// TestDisplayExplicitDisableSurvives: the zero value of Enabled is a real
// answer here, so it must not be mistaken for an absent key.
func TestDisplayExplicitDisableSurvives(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret+"display:\n  enabled: false\n")

	if c.Display.Enabled {
		t.Error("Display.Enabled = true, want the operator's explicit false")
	}
	// The rest of the section is still backfilled, so re-enabling it later
	// needs only the one key flipped.
	if c.Display.Address != defaultConfig.Display.Address || c.Display.PageSeconds != defaultConfig.Display.PageSeconds {
		t.Errorf("disabled section was not backfilled: %+v", c.Display)
	}
}

// TestDisplayPartialSectionIsBackfilled: an operator who writes only the key
// they care about gets a working display, not one at contrast 0 on an empty
// device path with zero-second pages.
func TestDisplayPartialSectionIsBackfilled(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret+"display:\n  flip: true\n")

	want := defaultConfig.Display
	want.Flip = true
	if c.Display != want {
		t.Errorf("Display = %+v, want %+v", c.Display, want)
	}
}

func TestDisplayExplicitValuesPassThrough(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret+`display:
  device: /dev/i2c-0
  address: "0x3d"
  brightness: 32
  pageSeconds: 9
  dimAfterMinutes: 3
`)

	want := Display{
		Enabled:         true,
		Device:          "/dev/i2c-0",
		Address:         "0x3d",
		Brightness:      32,
		PageSeconds:     9,
		DimAfterMinutes: 3,
	}
	if c.Display != want {
		t.Errorf("Display = %+v, want %+v", c.Display, want)
	}
}

// TestDisplayDefaultDeviceIsUnset pins that the default names no bus number.
// An empty device is what makes the display find its bus by controller
// address; a compiled-in "/dev/i2c-3" would have every machine this binary
// runs on writing display commands to whatever its third I2C bus is.
func TestDisplayDefaultDeviceIsUnset(t *testing.T) {
	if defaultConfig.Display.Device != "" {
		t.Errorf("defaultConfig.Display.Device = %q, want empty (auto-detect)", defaultConfig.Display.Device)
	}

	c, _ := loadConfigFromYAML(t, migrateSecret+"display:\n  flip: true\n")
	if c.Display.Device != "" {
		t.Errorf("Display.Device = %q after defaulting, want it left empty", c.Display.Device)
	}
}

// TestDisplayDimZeroMeansNever: 0 is "never dim", an explicit choice that a
// plain zero-value check would overwrite with the default.
func TestDisplayDimZeroMeansNever(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret+"display:\n  dimAfterMinutes: 0\n")

	if c.Display.DimAfterMinutes != 0 {
		t.Errorf("DimAfterMinutes = %d, want the operator's explicit 0", c.Display.DimAfterMinutes)
	}
}

// TestDisplayNonsenseValuesFallBack: zero brightness is an unreadable panel
// and a non-positive page time is no page at all; neither is a setting.
func TestDisplayNonsenseValuesFallBack(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret+`display:
  brightness: 0
  pageSeconds: -4
  dimAfterMinutes: -1
`)

	d, def := c.Display, defaultConfig.Display
	if d.Brightness != def.Brightness || d.PageSeconds != def.PageSeconds || d.DimAfterMinutes != def.DimAfterMinutes {
		t.Errorf("Display = %+v, want brightness/pageSeconds/dimAfterMinutes at their defaults", d)
	}

	// Out of range the other way. This must load at all: a brightness the
	// controller cannot take is a display problem, not a reason for the
	// BMC to refuse its whole configuration.
	c, _ = loadConfigFromYAML(t, migrateSecret+"display:\n  brightness: 300\n")
	if c.Display.Brightness != def.Brightness {
		t.Errorf("Brightness = %d for an out-of-range 300, want the default %d", c.Display.Brightness, def.Brightness)
	}
}

// TestDisplayAddressSurvivesSaveAsHex pins why Address is a string: saved as
// an integer, 0x3c would come back out of config.Save() as 60, and the file
// would no longer match the module's documentation or i2cdetect's output.
func TestDisplayAddressSurvivesSaveAsHex(t *testing.T) {
	c, _ := loadConfigFromYAML(t, migrateSecret)

	data, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{"display:", `address: "0x3c"`, `device: ""`, "dimAfterMinutes: 10"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshalled config does not carry %q:\n%s", want, data)
		}
	}
}
