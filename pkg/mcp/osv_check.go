package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const osvAPIURL = "https://api.osv.dev/v1/query"
const osvTimeout = 10 * time.Second

// osvHTTPClient is reused across calls for connection pooling.
var osvHTTPClient = &http.Client{Timeout: osvTimeout}

// osvQuery is the request body for the OSV API.
type osvQuery struct {
	Package osvPackage `json:"package"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

// osvResponse is the response from the OSV API.
type osvResponse struct {
	Vulns []osvVuln `json:"vulns"`
}

type osvVuln struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// CheckPackageForMalware checks if a command's package is known malware via the OSV API.
// Only checks npx/uvx/pipx commands. Returns nil if the package is safe or on any error
// (fail-open). Returns an error describing the threat if malware is detected.
func CheckPackageForMalware(command string, args []string) error {
	pkg, ecosystem := inferPackage(command, args)
	if pkg == "" || ecosystem == "" {
		return nil // not a package manager command, skip
	}

	malIDs, err := queryOSV(pkg, ecosystem)
	if err != nil {
		slog.Debug("osv check failed (fail-open)", "package", pkg, "error", err)
		return nil // fail-open
	}

	if len(malIDs) > 0 {
		return fmt.Errorf("mcp: blocked package %q (%s): known malware %s",
			pkg, ecosystem, strings.Join(malIDs, ", "))
	}
	return nil
}

// inferPackage extracts package name and ecosystem from a command invocation.
func inferPackage(command string, args []string) (pkg, ecosystem string) {
	base := command
	if idx := strings.LastIndexByte(command, '/'); idx >= 0 {
		base = command[idx+1:]
	}

	switch base {
	case "npx":
		ecosystem = "npm"
	case "uvx", "pipx":
		ecosystem = "PyPI"
	default:
		return "", ""
	}

	// Find the package name: first arg that doesn't start with '-'.
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && a != "--" {
			pkg = a
			// Strip version specifier: "foo@1.2.3" → "foo"
			if idx := strings.IndexByte(pkg, '@'); idx > 0 {
				pkg = pkg[:idx]
			}
			break
		}
	}

	return pkg, ecosystem
}

// queryOSV queries the OSV API and returns any MAL-* advisory IDs.
func queryOSV(pkg, ecosystem string) ([]string, error) {
	body, err := json.Marshal(osvQuery{
		Package: osvPackage{Name: pkg, Ecosystem: ecosystem},
	})
	if err != nil {
		return nil, err
	}

	resp, err := osvHTTPClient.Post(osvAPIURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv api returned %d", resp.StatusCode)
	}

	var result osvResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// Only flag MAL-* advisories (confirmed malware), ignore regular CVEs.
	var malIDs []string
	for _, v := range result.Vulns {
		if strings.HasPrefix(v.ID, "MAL-") {
			malIDs = append(malIDs, v.ID)
		}
	}
	return malIDs, nil
}
