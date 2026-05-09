package security

import (
	"os"
	"strings"
)

func platformMachineID() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		// Fallback for systems using dbus machine-id.
		data, err = os.ReadFile("/var/lib/dbus/machine-id")
		if err != nil {
			return ""
		}
	}
	return strings.TrimSpace(string(data))
}
