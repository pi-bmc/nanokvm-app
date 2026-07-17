package config

var defaultConfig = &Config{
	Proto: "http",
	Port: Port{
		Http:  80,
		Https: 443,
	},
	Cert: Cert{
		Crt: "server.crt",
		Key: "server.key",
	},
	Logger: Logger{
		Level: "info",
		File:  "stdout",
	},
	JWT: JWT{
		SecretKey:            "",
		RefreshTokenDuration: 2678400,
		RevokeTokensOnLogout: true,
	},
	Stun: "stun.l.google.com:19302",
	Turn: Turn{
		TurnAddr: "",
		TurnUser: "",
		TurnCred: "",
	},
	Authentication: "enable",
	Security: Security{
		LoginLockoutDuration: 0,
		LoginMaxFailures:     5,
	},
	IPMI: IPMI{
		Enabled: true,
		Port:    623,
	},
	Redfish: Redfish{
		Enabled: true,
	},
	Serial: Serial{
		Device:      "/dev/ttyS1",
		BaudRate:    115200,
		Parity:      "none",
		DataBits:    8,
		StopBits:    1,
		FlowControl: "none",
	},
	Firmware: Firmware{
		ImageURL:      "https://github.com/tinkerbell-community/uboot-raspberrypi/releases/download/v2026.04-rc4.1/uboot-raspberrypi-2026.04-rc4.1.img.xz",
		ImagePath:     "/data/firmware/uboot-rpi.img",
		FirmwareDir:   "/data/firmware/files",
		MountPoint:    "/data/firmware/mnt",
		MachineEnv:    "/data/firmware/files/machine.env",
		PersistentEnv: "/data/firmware/files/persistent.env",
		OnceEnv:       "/data/firmware/files/once.env",
		MediaDir:      "/data/media",
	},
	EfiVars: EfiVars{
		Enabled: true,
		// Read the store from the BMC's own i2c-slave-eeprom backing file. The
		// host (Raspberry Pi U-Boot, CONFIG_EFI_VARIABLE_I2C_STORE) writes the
		// variable blob into this EEPROM over I2C; the BMC reads/writes the same
		// bytes out-of-band through the slave device's backing file. This is
		// always safe — unlike raw /dev/i2c master access to 0x50, which would
		// address the BMC's *own* slave and cannot read the store.
		Path:      "/sys/bus/i2c/devices/0-1050/slave-eeprom",
		I2CBus:    -1, // disable the raw-master fallback
		I2CAddr:   0x50,
		PageSize:  64,
		StoreSize: 32768,
		// Durable mirror on /data (survives BMC reboots, unlike the volatile
		// i2c-slave-eeprom RAM buffer). Restored into the EEPROM at startup and
		// kept in sync with host/BMC writes.
		SnapshotPath: "/data/efivars/store.bin",
	},
	UbootEnv: UbootEnv{
		Enabled: true,
		// The same EEPROM as the UEFI variable store (see EfiVars): the host's
		// CONFIG_ENV_IS_IN_EEPROM writes its env partition at 0x4000 of that
		// 24c256, and the BMC reads/writes the same bytes out-of-band through
		// the slave device's backing file.
		Path:     "/sys/bus/i2c/devices/0-1050/slave-eeprom",
		I2CBus:   -1, // disable the raw-master fallback
		I2CAddr:  0x50,
		PageSize: 64,
		Offset:   0x4000, // host CONFIG_ENV_OFFSET
		Size:     0x4000, // host CONFIG_ENV_SIZE
		// Durable mirror on /data; see EfiVars.SnapshotPath.
		SnapshotPath: "/data/ubootenv/env.bin",
	},
	Power: Power{
		LegacyMode: false,
	},
	Telemetry: Telemetry{
		Enabled:     false,
		ServiceName: "nanokvm",
		Prometheus: Prometheus{
			Enabled: true,
			Path:    "/metrics",
		},
		OTLP: OTLP{
			Endpoint: "",
			Insecure: true,
		},
	},
	AutoUpdate: AutoUpdate{
		Enabled:         false,
		IntervalMinutes: 360, // 6 hours
		Application:     true,
		BIOS:            false,
	},
}

func checkDefaultValue() {
	needsPersist := false

	if instance.JWT.SecretKey == "" {
		instance.JWT.SecretKey = generateRandomSecretKey()
		instance.JWT.RevokeTokensOnLogout = true
		needsPersist = true
	}

	if instance.JWT.RefreshTokenDuration == 0 {
		instance.JWT.RefreshTokenDuration = 2678400
	}

	if instance.Stun == "" {
		instance.Stun = "stun.l.google.com:19302"
	}

	if instance.Authentication == "" {
		instance.Authentication = "enable"
	}

	// Apply serial defaults when not present in the config file.
	if instance.Serial.Device == "" {
		instance.Serial.Device = defaultConfig.Serial.Device
	}
	if instance.Serial.BaudRate == 0 {
		instance.Serial.BaudRate = defaultConfig.Serial.BaudRate
	}
	if instance.Serial.Parity == "" {
		instance.Serial.Parity = defaultConfig.Serial.Parity
	}
	if instance.Serial.DataBits == 0 {
		instance.Serial.DataBits = defaultConfig.Serial.DataBits
	}
	if instance.Serial.StopBits == 0 {
		instance.Serial.StopBits = defaultConfig.Serial.StopBits
	}
	if instance.Serial.FlowControl == "" {
		instance.Serial.FlowControl = defaultConfig.Serial.FlowControl
	}

	// Apply firmware defaults when not present in the config file.
	if instance.Firmware.ImageURL == "" {
		instance.Firmware.ImageURL = defaultConfig.Firmware.ImageURL
	}
	if instance.Firmware.ImagePath == "" {
		instance.Firmware.ImagePath = defaultConfig.Firmware.ImagePath
	}
	if instance.Firmware.FirmwareDir == "" {
		instance.Firmware.FirmwareDir = defaultConfig.Firmware.FirmwareDir
	}
	if instance.Firmware.MountPoint == "" {
		instance.Firmware.MountPoint = defaultConfig.Firmware.MountPoint
	}
	if instance.Firmware.MachineEnv == "" {
		instance.Firmware.MachineEnv = defaultConfig.Firmware.MachineEnv
	}
	if instance.Firmware.PersistentEnv == "" {
		instance.Firmware.PersistentEnv = defaultConfig.Firmware.PersistentEnv
	}
	if instance.Firmware.OnceEnv == "" {
		instance.Firmware.OnceEnv = defaultConfig.Firmware.OnceEnv
	}
	if instance.Firmware.MediaDir == "" {
		instance.Firmware.MediaDir = defaultConfig.Firmware.MediaDir
	}

	// Apply EFI variable store defaults when not present in the config file.
	// When neither a path nor an explicit non-zero master bus is configured,
	// default to the BMC's own i2c-slave-eeprom backing file (see defaultConfig).
	// This also upgrades legacy configs that persisted the old "i2cBus: 0"
	// (raw master to 0x50), which cannot read the BMC's own slave EEPROM.
	if instance.EfiVars.Path == "" && instance.EfiVars.I2CBus == 0 {
		instance.EfiVars.Path = defaultConfig.EfiVars.Path
		instance.EfiVars.I2CBus = defaultConfig.EfiVars.I2CBus
	}
	if instance.EfiVars.I2CAddr == 0 {
		instance.EfiVars.I2CAddr = defaultConfig.EfiVars.I2CAddr
	}
	if instance.EfiVars.PageSize <= 0 {
		instance.EfiVars.PageSize = defaultConfig.EfiVars.PageSize
	}
	if instance.EfiVars.StoreSize <= 0 {
		instance.EfiVars.StoreSize = defaultConfig.EfiVars.StoreSize
	}
	// Backfill the durable snapshot path for configs written before it existed,
	// so persistence is enabled on upgrade without editing server.yaml.
	if instance.EfiVars.SnapshotPath == "" {
		instance.EfiVars.SnapshotPath = defaultConfig.EfiVars.SnapshotPath
	}

	// Apply U-Boot env store defaults, mirroring the EfiVars handling above:
	// the environment lives at an offset of the same EEPROM.
	if instance.UbootEnv.Path == "" && instance.UbootEnv.I2CBus == 0 {
		instance.UbootEnv.Path = defaultConfig.UbootEnv.Path
		instance.UbootEnv.I2CBus = defaultConfig.UbootEnv.I2CBus
	}
	if instance.UbootEnv.I2CAddr == 0 {
		instance.UbootEnv.I2CAddr = defaultConfig.UbootEnv.I2CAddr
	}
	if instance.UbootEnv.PageSize <= 0 {
		instance.UbootEnv.PageSize = defaultConfig.UbootEnv.PageSize
	}
	if instance.UbootEnv.Offset <= 0 {
		instance.UbootEnv.Offset = defaultConfig.UbootEnv.Offset
	}
	if instance.UbootEnv.Size <= 0 {
		instance.UbootEnv.Size = defaultConfig.UbootEnv.Size
	}
	if instance.UbootEnv.SnapshotPath == "" {
		instance.UbootEnv.SnapshotPath = defaultConfig.UbootEnv.SnapshotPath
	}

	// The UEFI blob sits below the env partition on the same chip and is
	// otherwise bounded only by the whole-chip storeSize, so it could grow
	// into the environment. Clamp it at the env offset — the BMC-side mirror
	// of the cap U-Boot applies at CONFIG_ENV_OFFSET. This also upgrades
	// legacy configs that persisted the old whole-chip storeSize (32768).
	if instance.UbootEnv.Enabled && instance.EfiVars.Path == instance.UbootEnv.Path &&
		instance.EfiVars.StoreSize > instance.UbootEnv.Offset {
		instance.EfiVars.StoreSize = instance.UbootEnv.Offset
	}

	if instance.Telemetry.ServiceName == "" {
		instance.Telemetry.ServiceName = defaultConfig.Telemetry.ServiceName
	}
	if instance.Telemetry.Prometheus.Path == "" {
		instance.Telemetry.Prometheus.Path = defaultConfig.Telemetry.Prometheus.Path
	}

	if instance.AutoUpdate.IntervalMinutes <= 0 {
		instance.AutoUpdate.IntervalMinutes = defaultConfig.AutoUpdate.IntervalMinutes
	}

	instance.Hardware = getHardware()

	// Persist the generated secret key so tokens survive server restarts.
	if needsPersist {
		persistConfig()
	}
}
