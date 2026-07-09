package config

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

type Hardware struct {
	Version      HWVersion `yaml:"-"`
	GPIOReset    string    `yaml:"-"`
	GPIOPower    string    `yaml:"-"`
	GPIOPowerLED string    `yaml:"-"`
	GPIOHDDLed   string    `yaml:"-"`
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
	MountPoint    string `yaml:"mountPoint"`
	MachineEnv    string `yaml:"machineEnv"`    // read: effective env written by U-Boot
	PersistentEnv string `yaml:"persistentEnv"` // write: applied every boot
	OnceEnv       string `yaml:"onceEnv"`       // write: applied once then deleted
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
}
