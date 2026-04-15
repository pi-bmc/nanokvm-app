package ipmi

import (
	"os"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
)

// In-memory boot option storage.
var (
	bootMu          sync.Mutex
	bootDevice      byte // bits 5:2 boot device selector
	bootDeviceValid bool
)

// handleGetChassisStatus reads the power LED GPIO and returns chassis status.
func handleGetChassisStatus() []byte {
	conf := config.GetInstance().Hardware

	powerOn := false
	if conf.GPIOPowerLED != "" {
		on, err := gpioRead(conf.GPIOPowerLED)
		if err != nil {
			log.Errorf("IPMI: failed to read power LED: %s", err)
		} else {
			powerOn = on
		}
	}

	// Chassis Status response: completion code + 3 mandatory bytes
	// Byte 1: Current Power State
	//   bit 0: power is on
	// Byte 2: Last Power Event
	// Byte 3: Misc Chassis State
	resp := make([]byte, 4)
	resp[0] = ccOK
	if powerOn {
		resp[1] = 0x01 // system power is on
	}
	resp[2] = 0x00 // last power event: unknown
	resp[3] = 0x00 // misc: nothing special
	return resp
}

// handleChassisControl executes power/reset operations via GPIO.
func handleChassisControl(cmdData []byte) []byte {
	if len(cmdData) < 1 {
		return []byte{ccInvalidParam}
	}

	action := cmdData[0] & 0x0F
	conf := config.GetInstance().Hardware

	switch action {
	case controlPowerUp:
		log.Info("IPMI: chassis power on (short press)")
		go func() {
			if err := gpioWrite(conf.GPIOPower, 800*time.Millisecond); err != nil {
				log.Errorf("IPMI: power on failed: %s", err)
			}
		}()

	case controlPowerDown:
		log.Info("IPMI: chassis power off (long press)")
		go func() {
			if err := gpioWrite(conf.GPIOPower, 5*time.Second); err != nil {
				log.Errorf("IPMI: power off failed: %s", err)
			}
		}()

	case controlPowerCycle:
		log.Info("IPMI: chassis power cycle")
		go func() {
			// Long press to power off, wait, then short press to power on
			if err := gpioWrite(conf.GPIOPower, 5*time.Second); err != nil {
				log.Errorf("IPMI: power cycle off failed: %s", err)
				return
			}
			time.Sleep(2 * time.Second)
			if err := gpioWrite(conf.GPIOPower, 800*time.Millisecond); err != nil {
				log.Errorf("IPMI: power cycle on failed: %s", err)
			}
		}()

	case controlHardReset:
		log.Info("IPMI: chassis hard reset")
		go func() {
			if err := gpioWrite(conf.GPIOReset, 800*time.Millisecond); err != nil {
				log.Errorf("IPMI: reset failed: %s", err)
			}
		}()

	case controlSoftShutdown:
		// Soft shutdown — same as short press
		log.Info("IPMI: chassis soft shutdown (short press)")
		go func() {
			if err := gpioWrite(conf.GPIOPower, 800*time.Millisecond); err != nil {
				log.Errorf("IPMI: soft shutdown failed: %s", err)
			}
		}()

	default:
		log.Debugf("IPMI: unsupported chassis control action: 0x%02x", action)
		return []byte{ccInvalidParam}
	}

	return []byte{ccOK}
}

// handleSetSystemBootOptions stores boot device override in memory.
func handleSetSystemBootOptions(cmdData []byte) []byte {
	if len(cmdData) < 1 {
		return []byte{ccInvalidParam}
	}

	paramSelector := cmdData[0] & 0x7F

	switch paramSelector {
	case bootParamSetInProgress:
		// Accept and ignore set-in-progress
		return []byte{ccOK}

	case bootParamBootFlags:
		// Boot flags data: at least 5 bytes
		// cmdData[0] = param selector
		// cmdData[1] = boot flags byte 1 (bit 7 = valid, bit 6 = persistent, bits 1:0 = lock-out)
		// cmdData[2] = boot flags byte 2 (bits 5:2 = boot device)
		// cmdData[3] = boot flags byte 3
		// cmdData[4] = boot flags byte 4
		// cmdData[5] = boot flags byte 5
		if len(cmdData) < 6 {
			return []byte{ccInvalidParam}
		}

		bootMu.Lock()
		bootDeviceValid = cmdData[1]&0x80 != 0
		bootDevice = cmdData[2] & 0x3C // extract bits 5:2
		bootMu.Unlock()

		log.Debugf("IPMI: set boot device=0x%02x valid=%v", bootDevice, bootDeviceValid)
		return []byte{ccOK}

	default:
		return []byte{ccOK}
	}
}

// handleGetSystemBootOptions returns the stored boot device override.
func handleGetSystemBootOptions(cmdData []byte) []byte {
	if len(cmdData) < 1 {
		return []byte{ccInvalidParam}
	}

	paramSelector := cmdData[0] & 0x7F

	switch paramSelector {
	case bootParamBootFlags:
		bootMu.Lock()
		dev := bootDevice
		valid := bootDeviceValid
		bootMu.Unlock()

		// Response: cc + param revision + param selector + data(5)
		resp := make([]byte, 8)
		resp[0] = ccOK
		resp[1] = 0x01 // parameter revision
		resp[2] = bootParamBootFlags
		if valid {
			resp[3] = 0x80 // valid
		}
		resp[4] = dev // boot device in bits 5:2
		// resp[5], resp[6], resp[7] = 0
		return resp

	default:
		// Return minimal valid response for unknown param selectors
		resp := make([]byte, 3)
		resp[0] = ccOK
		resp[1] = 0x01 // parameter revision
		resp[2] = paramSelector
		return resp
	}
}

// gpioWrite pulses a GPIO pin high for the given duration, then sets it low.
// Duplicated from vm package to avoid import cycles.
func gpioWrite(device string, duration time.Duration) error {
	if err := os.WriteFile(device, []byte("1"), 0o666); err != nil {
		log.Errorf("IPMI gpio write %s failed: %s", device, err)
		return err
	}

	time.Sleep(duration)

	if err := os.WriteFile(device, []byte("0"), 0o666); err != nil {
		log.Errorf("IPMI gpio write %s failed: %s", device, err)
		return err
	}

	return nil
}

// gpioRead reads a GPIO pin and returns true if the system is powered on.
// The power LED GPIO reads 0 when power is on.
func gpioRead(device string) (bool, error) {
	content, err := os.ReadFile(device)
	if err != nil {
		return false, err
	}

	s := string(content)
	if len(s) > 1 {
		s = s[:len(s)-1]
	}

	value, err := strconv.Atoi(s)
	if err != nil {
		return false, nil
	}

	// GPIO reads 0 when power is on
	return value == 0, nil
}
