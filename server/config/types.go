package config

import "fmt"

type Config struct {
	Proto          string   `yaml:"proto"`
	Port           Port     `yaml:"port"`
	Cert           Cert     `yaml:"cert"`
	Logger         Logger   `yaml:"logger"`
	Authentication string   `yaml:"authentication"`
	JWT            JWT      `yaml:"jwt"`
	Stun           string   `yaml:"stun"`
	Turn           Turn     `yaml:"turn"`
	Security       Security `yaml:"security"`
	IPMI           IPMI     `yaml:"ipmi"`
	Redfish        Redfish  `yaml:"redfish"`
	Serial         Serial   `yaml:"serial"`
	Firmware       Firmware `yaml:"firmware"`
	EfiVars        EfiVars  `yaml:"efiVars"`
	UbootEnv       UbootEnv `yaml:"ubootEnv"`

	Power      Power      `yaml:"power"`
	Telemetry  Telemetry  `yaml:"telemetry"`
	AutoUpdate AutoUpdate `yaml:"autoUpdate"`
	Hardware   Hardware   `yaml:"-"`
}

// AutoUpdate configures the background updater that periodically checks
// for new application and BIOS (U-Boot) releases and applies them when
// enabled. Disabled by default — opt-in via config or the settings dialog.
type AutoUpdate struct {
	// Enabled gates the whole subsystem; when false the ticker doesn't run.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// IntervalMinutes between check-and-apply runs. Clamped to >= 5 at runtime
	// so a misconfigured value can't hammer GitHub.
	IntervalMinutes int `yaml:"intervalMinutes" json:"intervalMinutes"`
	// Application toggles auto-updating the NanoKVM application package.
	Application bool `yaml:"application" json:"application"`
	// BIOS toggles auto-updating the U-Boot BIOS image.
	BIOS bool `yaml:"bios" json:"bios"`
}

// Telemetry holds OpenTelemetry + Prometheus configuration.
//
// When Enabled is true:
//   - Gin HTTP handlers are auto-instrumented (request count, latency, traces).
//   - If Prometheus.Enabled, the OTel Prometheus exporter is served at
//     Prometheus.Path on the existing HTTP server (default /metrics).
//   - If OTLP.Endpoint is non-empty, traces and metrics are exported via OTLP
//     gRPC to that endpoint (e.g. otel-collector:4317).
type Telemetry struct {
	Enabled     bool       `yaml:"enabled"`
	ServiceName string     `yaml:"serviceName"`
	Prometheus  Prometheus `yaml:"prometheus"`
	OTLP        OTLP       `yaml:"otlp"`
}

type Prometheus struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// OTLP configures the OpenTelemetry Protocol exporter (gRPC).
// Insecure=true sends plaintext (suitable for sidecar collectors on localhost).
type OTLP struct {
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
}

type Logger struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type Port struct {
	Http  int `yaml:"http"`
	Https int `yaml:"https"`
}

type Cert struct {
	Crt string `yaml:"crt"`
	Key string `yaml:"key"`
}

type JWT struct {
	SecretKey            string `yaml:"secretKey"`
	RefreshTokenDuration uint64 `yaml:"refreshTokenDuration"`
	RevokeTokensOnLogout bool   `yaml:"revokeTokensOnLogout"`
}

type Turn struct {
	TurnAddr string `yaml:"turnAddr"`
	TurnUser string `yaml:"turnUser"`
	TurnCred string `yaml:"turnCred"`
}

type Security struct {
	LoginLockoutDuration int `yaml:"loginLockoutDuration"`
	LoginMaxFailures     int `yaml:"loginMaxFailures"`
}

// GPIOPin identifies a GPIO line via the character-device (CONFIG_GPIO_CDEV)
// interface: a gpiochip plus the line's offset within that chip. This replaces
// the deprecated sysfs numbering (/sys/class/gpio/gpioN/value, CONFIG_GPIO_SYSFS).
//
// Chip may be a bare name ("gpiochip0") or a device path ("/dev/gpiochip0").
type GPIOPin struct {
	Chip string
	Line int
}

// IsZero reports whether the pin is unset (no chip configured).
func (p GPIOPin) IsZero() bool { return p.Chip == "" }

// String renders the pin as chip:line for logs and errors.
func (p GPIOPin) String() string {
	if p.IsZero() {
		return "<unset>"
	}
	return fmt.Sprintf("%s:%d", p.Chip, p.Line)
}

type Hardware struct {
	Version      HWVersion `yaml:"-"`
	GPIOReset    GPIOPin   `yaml:"-"`
	GPIOPower    GPIOPin   `yaml:"-"`
	GPIOPowerLED GPIOPin   `yaml:"-"`
	GPIOHDDLed   GPIOPin   `yaml:"-"`
}

// Power holds power-control configuration.
// LegacyMode opts into direct-GPIO control (cuts power pin directly) instead of
// the default button-press simulation via the power-LED header.
type Power struct {
	LegacyMode bool `yaml:"legacyMode"`
}

type IPMI struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type Redfish struct {
	Enabled bool `yaml:"enabled"`
}

type Serial struct {
	Device      string `yaml:"device"`
	BaudRate    int    `yaml:"baudRate"`
	Parity      string `yaml:"parity"`
	DataBits    int    `yaml:"dataBits"`
	StopBits    int    `yaml:"stopBits"`
	FlowControl string `yaml:"flowControl"`
}

type Firmware struct {
	ImageURL  string `yaml:"imageURL"`
	ImagePath string `yaml:"imagePath"`
	// FirmwareDir is the local directory holding the canonical FAT root files
	// (u-boot.bin, config.txt, RPi *.elf/*.dat firmware blobs, .dtb files,
	// overlays/, etc.). The boot image is built from this directory; it is
	// the source of truth, allowing each file to be versioned/edited
	// independently of the composite .img.
	FirmwareDir string `yaml:"firmwareDir"`
	// MountPoint is retained for backward-compat with existing YAML files but
	// is no longer used at runtime — env paths are derived as FAT-root names.
	MountPoint string `yaml:"mountPoint"`
	// MachineEnv, PersistentEnv and OnceEnv are retained for backward-compat
	// with existing YAML files but are no longer used at runtime: the U-Boot
	// environment lives in the I2C EEPROM (see UbootEnv), not in files inside
	// the boot image.
	MachineEnv    string `yaml:"machineEnv"`
	PersistentEnv string `yaml:"persistentEnv"`
	OnceEnv       string `yaml:"onceEnv"`
	// MediaDir is the directory where ISO images for virtual media are stored.
	MediaDir string `yaml:"mediaDir"`
}

// EfiVars configures access to the UEFI variable store that U-Boot on the
// host persists in an I2C EEPROM (CONFIG_EFI_VARIABLE_I2C_STORE). The BMC
// reads and rewrites BootOrder/BootNext there out-of-band.
type EfiVars struct {
	// Enabled gates the subsystem; when false Redfish boot overrides fall
	// back to the U-Boot env files.
	Enabled bool `yaml:"enabled"`
	// Path is a file-backed store: the backing file of a kernel
	// i2c-slave-eeprom device (BMC emulating the EEPROM, e.g.
	// /sys/bus/i2c/devices/0-1050/slave-eeprom), an at24 sysfs eeprom node,
	// or a plain file for testing. Takes precedence over I2CBus.
	Path string `yaml:"path"`
	// I2CBus selects raw /dev/i2c-N master access when Path is empty.
	// Set to -1 to disable.
	I2CBus int `yaml:"i2cBus"`
	// I2CAddr is the EEPROM chip address (default 0x50).
	I2CAddr int `yaml:"i2cAddr"`
	// PageSize is the EEPROM write page size in bytes (default 64, 24c256).
	PageSize int `yaml:"pageSize"`
	// StoreSize caps the variable blob size in bytes (default 32768, 24c256).
	StoreSize int `yaml:"storeSize"`
	// SnapshotPath is a durable file on persistent storage that mirrors the
	// store. The kernel i2c-slave-eeprom backing the EEPROM is volatile RAM,
	// wiped on every BMC reboot; the app restores this snapshot into it at
	// startup and re-saves it whenever the host (or the BMC) changes the
	// store, so BootOrder/BootNext survive BMC reboots. Empty disables
	// persistence.
	SnapshotPath string `yaml:"snapshotPath"`
}

// UbootEnv configures where the U-Boot environment lives. U-Boot
// (CONFIG_ENV_IS_IN_EEPROM) keeps it at a fixed offset of the *same* EEPROM
// that holds the UEFI variable store, so this mirrors EfiVars' access fields
// and adds the region within the device:
//
//	0x0000..0x3fff  UEFI variable blob (EfiVars)
//	0x4000..0x7fff  U-Boot environment (this store)
type UbootEnv struct {
	// Enabled gates the subsystem; when false the environment API reports the
	// store as unavailable.
	Enabled bool `yaml:"enabled"`
	// Path is a file-backed store: the backing file of a kernel
	// i2c-slave-eeprom device (BMC emulating the EEPROM), an at24 sysfs
	// eeprom node, or a plain file for testing. Takes precedence over I2CBus.
	Path string `yaml:"path"`
	// I2CBus selects raw /dev/i2c-N master access when Path is empty.
	// Set to -1 to disable.
	I2CBus int `yaml:"i2cBus"`
	// I2CAddr is the EEPROM chip address (default 0x50).
	I2CAddr int `yaml:"i2cAddr"`
	// PageSize is the EEPROM write page size in bytes (default 64, 24c256).
	PageSize int `yaml:"pageSize"`
	// Offset is where the env partition starts in the EEPROM. Must match the
	// host's CONFIG_ENV_OFFSET (default 0x4000).
	Offset int `yaml:"offset"`
	// Size is the env partition size, including its CRC32 header. Must match
	// the host's CONFIG_ENV_SIZE (default 0x4000).
	Size int `yaml:"size"`
	// SnapshotPath is a durable file mirroring the env region. The kernel
	// i2c-slave-eeprom backing the EEPROM is volatile RAM wiped on every BMC
	// reboot; the app restores this snapshot at startup and re-saves it
	// whenever the host (saveenv) or the BMC changes the environment, so it
	// survives BMC reboots. Empty disables persistence.
	SnapshotPath string `yaml:"snapshotPath"`
}
