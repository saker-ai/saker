package skillhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunCommand handles "saker skill <action> ..." subcommands.
//
// Actions:
//
//	login [--registry URL]        — device flow; persists token + handle
//	logout                         — clear token from settings.local.json
//	whoami                         — print identity tied to current token
//	search <query>                 — search registry
//	list [--category X]            — list skills
//	install <slug> [--version V]   — install to <project>/.saker/subscribed-skills
//	uninstall <slug>               — remove installed skill dir
//	publish <dir> [--slug S] [--version V] [--kind K]
//	                                — publish a local skill directory
//	publish-learned <dir>          — publish under <handle>/learned-<name>
//	sync                           — refresh all Subscriptions with ETag
func RunCommand(stdout, stderr io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		return PrintUsage(stdout)
	}

	cfg, err := LoadFromProject(projectRoot)
	if err != nil {
		return fmt.Errorf("load skillhub config: %w", err)
	}

	action := args[0]
	rest := args[1:]

	switch action {
	case "help", "-h", "--help":
		return PrintUsage(stdout)
	case "login":
		return RunLogin(stdout, stderr, projectRoot, cfg, rest)
	case "logout":
		return RunLogout(stdout, projectRoot, cfg)
	case "whoami":
		return RunWhoAmI(stdout, cfg.Resolved())
	case "search":
		return RunSearch(stdout, cfg.Resolved(), rest)
	case "list":
		return RunList(stdout, cfg.Resolved(), rest)
	case "get":
		return RunGet(stdout, cfg.Resolved(), rest)
	case "install":
		return RunInstall(stdout, projectRoot, cfg.Resolved(), rest)
	case "uninstall":
		return RunUninstall(stdout, projectRoot, rest)
	case "publish":
		return RunPublish(stdout, cfg.Resolved(), rest)
	case "publish-learned":
		return RunPublishLearned(stdout, cfg.Resolved(), rest)
	case "sync":
		return RunSync(stdout, projectRoot, cfg.Resolved())
	default:
		return fmt.Errorf("unknown skill action: %s (try 'saker skill help')", action)
	}
}

// PrintUsage writes skill subcommand help to the given writer.
func PrintUsage(out io.Writer) error {
	fmt.Fprintln(out, `Usage: saker skill <action> [args]

Actions:
  login [--registry URL]          Device-flow login to skillhub
  logout                           Clear stored token
  whoami                           Print identity for current token
  search <query>                   Search skills (keyword)
  list [--category X] [--sort S]   List skills with pagination
  get <slug>                       Show metadata for a single skill
  install <slug> [--version V]     Install to .saker/subscribed-skills/
  uninstall <slug>                 Remove an installed skill
  publish <dir> --slug S [--version V] [--kind K]
                                   Upload a skill directory
  publish-learned <dir>            Auto-publish under <handle>/learned-<name>
  sync                             Refresh all subscribed skills (ETag-aware)

Config is read from .saker/settings.json and settings.local.json under key
"skillhub". Environment overrides: SKILLHUB_REGISTRY, SKILLHUB_TOKEN,
SKILLHUB_OFFLINE.`)
	return nil
}

// --- login / logout / whoami -----------------------------------------------

// RunLogin performs the device-flow login against the skillhub registry.
func RunLogin(stdout, stderr io.Writer, projectRoot string, cfg Config, args []string) error {
	registry := cfg.Registry
	interval := 5 * time.Second
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--registry":
			if i+1 >= len(args) {
				return errors.New("--registry requires a value")
			}
			registry = args[i+1]
			i++
		case "--interval":
			if i+1 >= len(args) {
				return errors.New("--interval requires a duration")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid --interval: %w", err)
			}
			interval = d
			i++
		}
	}
	if strings.TrimSpace(registry) == "" {
		registry = DefaultRegistry
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := New(registry)
	dc, err := client.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}

	fmt.Fprintf(stdout, "\nOpen in your browser:\n  %s\n", dc.VerificationURL)
	fmt.Fprintf(stdout, "User code: %s\n", dc.UserCode)
	fmt.Fprintf(stdout, "Waiting for approval (polling every %s)...\n\n", interval)

	token, err := client.PollDeviceToken(ctx, dc.DeviceCode, interval)
	if err != nil {
		return fmt.Errorf("poll device token: %w", err)
	}

	client.SetToken(token)
	who, err := client.WhoAmI(ctx)
	if err != nil {
		// Token accepted by server but whoami failed — still save it; the user
		// can debug via `saker skill whoami` next.
		fmt.Fprintf(stderr, "warning: whoami failed: %v\n", err)
	}

	cfg.Registry = strings.TrimRight(registry, "/")
	cfg.Token = token
	if who != nil && who.Handle != "" {
		cfg.Handle = who.Handle
	}
	if err := SaveToProject(projectRoot, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if who != nil {
		fmt.Fprintf(stdout, "Logged in as %s (%s)\n", who.Handle, who.ID)
	} else {
		fmt.Fprintln(stdout, "Token saved.")
	}
	return nil
}

// RunLogout clears the stored skillhub token.
func RunLogout(stdout io.Writer, projectRoot string, cfg Config) error {
	if cfg.Token == "" && cfg.Handle == "" {
		fmt.Fprintln(stdout, "No skillhub credentials found.")
		return nil
	}
	cfg.Token = ""
	// keep handle as a hint for next login prompt
	if err := SaveToProject(projectRoot, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(stdout, "Skillhub token cleared.")
	return nil
}

// RunWhoAmI prints the identity tied to the current token.
func RunWhoAmI(stdout io.Writer, cfg Config) error {
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	who, err := client.WhoAmI(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ID:     %s\nHandle: %s\nEmail:  %s\nRole:   %s\n", who.ID, who.Handle, who.Email, who.Role)
	return nil
}

// --- search / list / get ---------------------------------------------------

// RunSearch searches the skillhub registry by keyword.
func RunSearch(stdout io.Writer, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill search <query>")
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := client.Search(ctx, strings.Join(args, " "), 20)
	if err != nil {
		return err
	}
	if len(res.Hits) == 0 {
		fmt.Fprintln(stdout, "No results.")
		return nil
	}
	for _, h := range res.Hits {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", h.Slug, h.DisplayName, h.Summary)
	}
	return nil
}

// RunList lists skills with optional category/sort/cursor/limit filters.
func RunList(stdout io.Writer, cfg Config, args []string) error {
	category, sort, cursor := "", "", ""
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--category":
			if i+1 < len(args) {
				category = args[i+1]
				i++
			}
		case "--sort":
			if i+1 < len(args) {
				sort = args[i+1]
				i++
			}
		case "--cursor":
			if i+1 < len(args) {
				cursor = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				if n, err := parseInt(args[i+1]); err == nil {
					limit = n
				}
				i++
			}
		}
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := client.List(ctx, category, sort, cursor, limit)
	if err != nil {
		return err
	}
	for _, s := range res.Data {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", s.Slug, s.DisplayName, s.Category)
	}
	if res.NextCursor != "" {
		fmt.Fprintf(stdout, "\n# next cursor: %s\n", res.NextCursor)
	}
	return nil
}

// RunGet shows metadata for a single skill by slug.
func RunGet(stdout io.Writer, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill get <slug>")
	}
	slug := args[0]
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := client.Get(ctx, slug)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return nil
}

// --- install / uninstall / sync --------------------------------------------

// RunInstall downloads and installs a skill by slug.
func RunInstall(stdout io.Writer, projectRoot string, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill install <slug> [--version V]")
	}
	slug := args[0]
	version := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--version" && i+1 < len(args) {
			version = args[i+1]
			i++
		}
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := client.Install(ctx, slug, InstallOptions{
		Dir:     SubscribedDir(projectRoot),
		Version: version,
	})
	if err != nil {
		return err
	}

	// Record subscription so `saker skill sync` refreshes it.
	if err := addSubscription(projectRoot, cfg, slug); err != nil {
		fmt.Fprintf(stdout, "warning: could not persist subscription: %v\n", err)
	}

	fmt.Fprintf(stdout, "Installed %s (%s) → %s (%d files)\n", res.Slug, res.Version, res.Dir, res.FilesCount)
	return nil
}

// RunUninstall removes a previously installed skill.
func RunUninstall(stdout io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill uninstall <slug>")
	}
	slug := args[0]
	if err := Uninstall(SubscribedDir(projectRoot), slug); err != nil {
		return err
	}
	// Remove from subscriptions list.
	cfg, _ := LoadFromProject(projectRoot)
	cfg.Subscriptions = removeString(cfg.Subscriptions, slug)
	_ = SaveToProject(projectRoot, cfg)
	fmt.Fprintf(stdout, "Uninstalled %s\n", slug)
	return nil
}

// RunSync refreshes all subscribed skills using ETag-aware downloads.
func RunSync(stdout io.Writer, projectRoot string, cfg Config) error {
	if len(cfg.Subscriptions) == 0 {
		fmt.Fprintln(stdout, "No subscriptions configured.")
		return nil
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	root := SubscribedDir(projectRoot)
	for _, slug := range cfg.Subscriptions {
		etag := readOriginETag(root, slug)
		res, err := client.Install(ctx, slug, InstallOptions{Dir: root, ETag: etag})
		if err != nil {
			fmt.Fprintf(stdout, "FAIL\t%s\t%v\n", slug, err)
			continue
		}
		if res.NotModified {
			fmt.Fprintf(stdout, "UP-TO-DATE\t%s\n", slug)
			continue
		}
		fmt.Fprintf(stdout, "UPDATED\t%s\t%s\t(%d files)\n", slug, res.Version, res.FilesCount)
	}
	return nil
}

// --- publish ---------------------------------------------------------------

// RunPublish uploads a skill directory to the registry.
func RunPublish(stdout io.Writer, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill publish <dir> --slug S [--version V] [--kind K]")
	}
	dir := args[0]
	slug, version, kind, changelog := "", "0.0.1", "custom", ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--slug":
			if i+1 < len(args) {
				slug = args[i+1]
				i++
			}
		case "--version":
			if i+1 < len(args) {
				version = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--changelog":
			if i+1 < len(args) {
				changelog = args[i+1]
				i++
			}
		}
	}
	if slug == "" {
		return errors.New("--slug is required")
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	files, err := CollectDirFiles(dir, 0)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := client.Publish(ctx, PublishRequest{
		Slug:      slug,
		Version:   version,
		Kind:      kind,
		Changelog: changelog,
		Files:     files,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Published %s@%s (fingerprint=%s)\n", resp.Skill.Slug, resp.Version.Version, resp.Version.Fingerprint)
	return nil
}

// RunPublishLearned auto-publishes a learned skill under the user's namespace.
func RunPublishLearned(stdout io.Writer, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill publish-learned <dir>")
	}
	if cfg.Handle == "" {
		return errors.New("no handle configured; run 'saker skill login' first")
	}
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := client.PublishLearned(ctx, args[0], PublishLearnedOptions{
		Handle:     cfg.Handle,
		Visibility: cfg.LearnedVisibility,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Published learned skill %s@%s\n", resp.Skill.Slug, resp.Version.Version)
	return nil
}

// --- helpers ---------------------------------------------------------------

// NewClient creates a skillhub Client from the resolved Config.
// Returns an error if the config indicates offline mode or has no registry.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Offline {
		return nil, errors.New("skillhub is in offline mode (SKILLHUB_OFFLINE=1)")
	}
	if strings.TrimSpace(cfg.Registry) == "" {
		return nil, errors.New("no registry configured; set SKILLHUB_REGISTRY or .saker/settings.json skillhub.registry")
	}
	opts := []ClientOption{}
	if cfg.Token != "" {
		opts = append(opts, WithToken(cfg.Token))
	}
	return New(cfg.Registry, opts...), nil
}

// addSubscription appends slug to cfg.Subscriptions (dedup) and persists.
func addSubscription(projectRoot string, cfg Config, slug string) error {
	for _, s := range cfg.Subscriptions {
		if s == slug {
			return nil
		}
	}
	cfg.Subscriptions = append(cfg.Subscriptions, slug)
	return SaveToProject(projectRoot, cfg)
}

func removeString(list []string, target string) []string {
	out := list[:0]
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

// readOriginETag reads the ETag recorded by Install's .skillhub-origin file.
// Returns empty string if not found (forces a fresh download).
func readOriginETag(root, slug string) string {
	dir := strings.ReplaceAll(slug, "/", "__")
	data, err := os.ReadFile(filepath.Join(root, dir, ".skillhub-origin"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "etag=") {
			return strings.TrimPrefix(line, "etag=")
		}
	}
	return ""
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
