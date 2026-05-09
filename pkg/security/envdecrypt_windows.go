package security

import (
	"os/exec"
	"strings"
)

func platformMachineID() string {
	out, err := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`,
		"/v", "MachineGuid").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "MachineGuid") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				return strings.TrimSpace(fields[len(fields)-1])
			}
		}
	}
	return ""
}
