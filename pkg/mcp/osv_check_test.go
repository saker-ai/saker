package mcp

import "testing"

func TestInferPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		args    []string
		wantPkg string
		wantEco string
	}{
		{"npx basic", "npx", []string{"typescript-mcp"}, "typescript-mcp", "npm"},
		{"npx with version", "npx", []string{"foo@1.2.3"}, "foo", "npm"},
		{"npx with flags", "npx", []string{"-y", "my-server"}, "my-server", "npm"},
		{"uvx basic", "uvx", []string{"my-tool"}, "my-tool", "PyPI"},
		{"pipx basic", "pipx", []string{"run", "my-tool"}, "run", "PyPI"},
		{"full path npx", "/usr/local/bin/npx", []string{"my-pkg"}, "my-pkg", "npm"},
		{"not a package manager", "node", []string{"server.js"}, "", ""},
		{"go run", "go", []string{"run", "."}, "", ""},
		{"no args", "npx", nil, "", "npm"},
		{"only flags", "npx", []string{"--yes"}, "", "npm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pkg, eco := inferPackage(tt.command, tt.args)
			if pkg != tt.wantPkg {
				t.Errorf("inferPackage(%q, %v) pkg = %q, want %q", tt.command, tt.args, pkg, tt.wantPkg)
			}
			if eco != tt.wantEco {
				t.Errorf("inferPackage(%q, %v) ecosystem = %q, want %q", tt.command, tt.args, eco, tt.wantEco)
			}
		})
	}
}
