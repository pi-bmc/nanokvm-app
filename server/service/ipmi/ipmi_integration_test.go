package ipmi

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	testUser = "admin"
	testPass = "admin"
)

// findFreePort returns an available UDP port.
func findFreePort(t *testing.T) int {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

// startServer starts an IPMI server on a random port and returns the server and port.
func startServer(t *testing.T) (*Server, int) {
	t.Helper()
	port := findFreePort(t)
	srv, err := Start(port)
	if err != nil {
		t.Fatalf("start IPMI server: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })
	// Give the listener goroutine a moment to start.
	time.Sleep(50 * time.Millisecond)
	return srv, port
}

// ipmitoolPath returns the path to ipmitool, skipping if not found.
func ipmitoolPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ipmitool")
	if err != nil {
		t.Skip("ipmitool not found in PATH, skipping integration test")
	}
	return path
}

// runIPMITool executes ipmitool with common flags and returns combined output.
func runIPMITool(t *testing.T, port int, cipher int, args ...string) (string, error) {
	t.Helper()
	tool := ipmitoolPath(t)
	baseArgs := []string{
		"-I", "lanplus",
		"-H", "127.0.0.1",
		"-p", fmt.Sprintf("%d", port),
		"-U", testUser,
		"-P", testPass,
		"-C", fmt.Sprintf("%d", cipher),
	}
	fullArgs := append(baseArgs, args...)
	t.Logf("ipmitool %s", strings.Join(fullArgs, " "))

	cmd := exec.Command(tool, fullArgs...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("output: %s", output)
	if err != nil {
		t.Logf("error: %v", err)
	}
	return output, err
}

// TestIPMI_ChassisStatus_Cipher0 tests session establishment and chassis status
// using cipher suite 0 (no auth, no integrity, no encryption).
func TestIPMI_ChassisStatus_Cipher0(t *testing.T) {
	_, port := startServer(t)
	out, err := runIPMITool(t, port, 0, "chassis", "status")
	if err != nil {
		t.Fatalf("ipmitool chassis status failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "System Power") {
		t.Errorf("expected 'System Power' in output, got: %s", out)
	}
}

// TestIPMI_ChassisStatus_Cipher2 tests with cipher suite 2
// (HMAC-SHA1 / HMAC-SHA1-96 / no encryption).
func TestIPMI_ChassisStatus_Cipher2(t *testing.T) {
	_, port := startServer(t)
	out, err := runIPMITool(t, port, 2, "chassis", "status")
	if err != nil {
		t.Fatalf("ipmitool chassis status failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "System Power") {
		t.Errorf("expected 'System Power' in output, got: %s", out)
	}
}

// TestIPMI_ChassisControl tests chassis power control commands.
func TestIPMI_ChassisControl(t *testing.T) {
	_, port := startServer(t)

	tests := []struct {
		name string
		args []string
	}{
		{"power_on", []string{"chassis", "power", "on"}},
		{"power_off", []string{"chassis", "power", "off"}},
		{"power_cycle", []string{"chassis", "power", "cycle"}},
		{"power_reset", []string{"chassis", "power", "reset"}},
		{"soft_shutdown", []string{"chassis", "power", "soft"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runIPMITool(t, port, 0, tc.args...)
			if err != nil {
				t.Fatalf("ipmitool %v failed: %v\noutput: %s", tc.args, err, out)
			}
		})
	}
}

// TestIPMI_BootDevice tests setting and getting boot device.
func TestIPMI_BootDevice(t *testing.T) {
	_, port := startServer(t)

	// Set boot device to PXE
	out, err := runIPMITool(t, port, 0, "chassis", "bootdev", "pxe")
	if err != nil {
		t.Fatalf("set bootdev pxe failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Set Boot Device to pxe") {
		t.Errorf("expected set boot device confirmation, got: %s", out)
	}

	// Set boot device to disk
	out, err = runIPMITool(t, port, 0, "chassis", "bootdev", "disk")
	if err != nil {
		t.Fatalf("set bootdev disk failed: %v\noutput: %s", err, out)
	}
}

// TestIPMI_SessionClose tests that sessions can be cleanly closed.
func TestIPMI_SessionClose(t *testing.T) {
	_, port := startServer(t)

	// A normal chassis status command opens and closes a session.
	// Running it twice exercises session cleanup.
	for i := 0; i < 3; i++ {
		out, err := runIPMITool(t, port, 0, "chassis", "status")
		if err != nil {
			t.Fatalf("iteration %d: ipmitool chassis status failed: %v\noutput: %s", i, err, out)
		}
	}
}

// TestIPMI_MCInfo tests Get Device ID (mc info) command.
func TestIPMI_MCInfo(t *testing.T) {
	_, port := startServer(t)
	out, err := runIPMITool(t, port, 0, "mc", "info")
	if err != nil {
		t.Fatalf("ipmitool mc info failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Device ID") {
		t.Errorf("expected 'Device ID' in output, got: %s", out)
	}
	if !strings.Contains(out, "IPMI Version") {
		t.Errorf("expected 'IPMI Version' in output, got: %s", out)
	}
}

// TestIPMI_ChassisControl_Cipher2 tests chassis control with integrity enabled.
func TestIPMI_ChassisControl_Cipher2(t *testing.T) {
	_, port := startServer(t)
	out, err := runIPMITool(t, port, 2, "chassis", "power", "status")
	if err != nil {
		t.Fatalf("ipmitool power status cipher 2 failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Chassis Power is") {
		t.Errorf("expected 'Chassis Power is' in output, got: %s", out)
	}
}
