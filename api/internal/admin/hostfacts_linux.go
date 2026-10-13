//go:build linux

package admin

import (
	"os"
	"strings"
	"syscall"
)

// timeError is the clock state adjtimex(2) reports while the kernel clock is
// not synchronized to a time source (TIME_ERROR).
const timeError = 5

// hostFacts reads the two INSTALL.md settings the API can see on Linux: an
// NTP-synchronized clock (§4.1 — TOTP codes and audit timestamps depend on
// it) and the SELinux mode (§7).
func hostFacts() HostFacts {
	var f HostFacts

	// A read-only adjtimex (no mode bits) needs no privilege and works for
	// chronyd and ntpd alike.
	var tx syscall.Timex
	if state, err := syscall.Adjtimex(&tx); err == nil {
		synced := state != timeError
		f.ClockSynchronized = &synced
	}

	mode := "disabled"
	if raw, err := os.ReadFile("/sys/fs/selinux/enforce"); err == nil {
		switch strings.TrimSpace(string(raw)) {
		case "1":
			mode = "enforcing"
		case "0":
			mode = "permissive"
		}
	} else if !os.IsNotExist(err) {
		return f // present but unreadable: unknown
	}
	f.SELinux = &mode
	return f
}
