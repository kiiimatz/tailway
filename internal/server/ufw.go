package server

import (
	"fmt"
	"os/exec"
	"runtime"
)

// ufwProto returns the UFW protocol string for a tunnel protocol.
func ufwProto(tunnelProto string) string {
	if isUDPBased(tunnelProto) {
		return "udp"
	}
	return "tcp"
}

// ufwActive reports whether UFW is installed and currently enabled on this
// machine. Returns false on non-Linux systems or if ufw is not found.
func ufwActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("ufw"); err != nil {
		return false
	}
	// "ufw status" exits 0 even when inactive; parse stdout.
	out, err := exec.Command("ufw", "status").Output()
	if err != nil {
		// Try with sudo in case the user can't read ufw status directly.
		out, err = exec.Command("sudo", "ufw", "status").Output()
		if err != nil {
			return false
		}
	}
	// Output starts with "Status: active" when enabled.
	for i := 0; i < len(out)-7; i++ {
		if string(out[i:i+13]) == "Status: activ" {
			return true
		}
	}
	return false
}

// ufwRun executes a ufw command, trying without sudo first and falling back
// to sudo if the direct call fails (e.g. permission denied).
func ufwRun(args ...string) error {
	if err := exec.Command("ufw", args...).Run(); err == nil {
		return nil
	}
	return exec.Command("sudo", append([]string{"ufw"}, args...)...).Run()
}

// ufwAllow opens port/proto in UFW and reloads the rule set.
func ufwAllow(port int, proto string) error {
	return ufwRun("allow", fmt.Sprintf("%d/%s", port, proto))
}

// ufwDelete removes the allow rule for port/proto.
func ufwDelete(port int, proto string) error {
	return ufwRun("delete", "allow", fmt.Sprintf("%d/%s", port, proto))
}
