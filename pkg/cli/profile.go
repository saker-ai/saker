package cli

import (
	"fmt"
	"io"

	"github.com/saker-ai/saker/pkg/profile"
)

// runProfileCommand handles "saker profile <action> [name]" subcommands.
func runProfileCommand(stdout, stderr io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		// "saker profile" with no args → show active profile.
		active := profile.GetActive(projectRoot)
		if active == "" {
			active = "default"
		}
		fmt.Fprintf(stdout, "Active profile: %s\n", active)
		fmt.Fprintf(stdout, "Profile dir: %s\n", profile.Dir(projectRoot, active))
		return nil
	}

	action := args[0]
	switch action {
	case "list":
		profiles, err := profile.List(projectRoot)
		if err != nil {
			return fmt.Errorf("profile list: %w", err)
		}
		for _, p := range profiles {
			marker := "  "
			if p.IsDefault {
				marker = "* "
			} else if p.Name == profile.GetActive(projectRoot) {
				marker = "* "
			}
			model := ""
			if p.Model != "" {
				model = " (model: " + p.Model + ")"
			}
			fmt.Fprintf(stdout, "%s%s%s\n", marker, p.Name, model)
		}
		return nil

	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile create <name> [--clone <source>]")
		}
		name := args[1]
		opts := profile.CreateOptions{}
		for i := 2; i < len(args); i++ {
			if args[i] == "--clone" && i+1 < len(args) {
				opts.CloneFrom = args[i+1]
				i++
			}
		}
		if err := profile.Create(projectRoot, name, opts); err != nil {
			return fmt.Errorf("profile create: %w", err)
		}
		fmt.Fprintf(stdout, "Created profile: %s\n", name)
		fmt.Fprintf(stdout, "  Path: %s\n", profile.Dir(projectRoot, name))
		return nil

	case "use":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile use <name>")
		}
		name := args[1]
		if name != "default" && !profile.Exists(projectRoot, name) {
			return fmt.Errorf("profile %q does not exist (use 'saker profile create %s' first)", name, name)
		}
		if err := profile.SetActive(projectRoot, name); err != nil {
			return fmt.Errorf("profile use: %w", err)
		}
		if name == "default" {
			fmt.Fprintln(stdout, "Switched to default profile")
		} else {
			fmt.Fprintf(stdout, "Switched to profile: %s\n", name)
		}
		return nil

	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile delete <name>")
		}
		name := args[1]
		if err := profile.Delete(projectRoot, name); err != nil {
			return fmt.Errorf("profile delete: %w", err)
		}
		fmt.Fprintf(stdout, "Deleted profile: %s\n", name)
		return nil

	case "show":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		} else {
			name = profile.GetActive(projectRoot)
		}
		if name == "" {
			name = "default"
		}
		fmt.Fprintf(stdout, "Profile: %s\n", name)
		fmt.Fprintf(stdout, "  Path: %s\n", profile.Dir(projectRoot, name))
		fmt.Fprintf(stdout, "  Exists: %v\n", profile.Exists(projectRoot, name))
		return nil

	default:
		return fmt.Errorf("unknown profile action: %s (use list, create, use, delete, show)", action)
	}
}
