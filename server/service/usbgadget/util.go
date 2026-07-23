package usbgadget

import (
	"bytes"
	"os"
	"strings"
)

// writeAttr writes value to a configfs attribute file verbatim. configfs is
// lenient about trailing newlines.
func writeAttr(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o644)
}

// writeAttrIfDifferent writes value only when the file's current (trimmed)
// contents differ. This avoids EBUSY when re-asserting unchanged descriptor
// fields on an already-bound gadget — the common server-restart case where the
// gadget already exists and must not be disturbed.
func writeAttrIfDifferent(path, value string) error {
	cur, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(cur)) == strings.TrimSpace(value) {
		return nil
	}
	return writeAttr(path, value)
}

// writeReportDesc writes an HID report descriptor, skipping the write when the
// existing contents already match byte-for-byte.
func writeReportDesc(path string, desc []byte) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, desc) {
		return nil
	}
	return os.WriteFile(path, desc, 0o644)
}

// isMountPoint reports whether path is a mount point per /proc/mounts.
func isMountPoint(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}
