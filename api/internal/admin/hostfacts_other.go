//go:build !linux

package admin

// hostFacts has nothing to report off Linux: the clock state and SELinux
// are Linux interfaces, and production runs on RHEL (INSTALL.md §2).
func hostFacts() HostFacts { return HostFacts{} }
