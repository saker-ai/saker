package main

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

	"github.com/cinience/saker/pkg/skillhub"
)

// runSkillCommand handles "saker skill <action> ..." subcommands.
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
func runSkillCommand(stdout, stderr io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		return printSkillUsage(stdout)
	}

	cfg, err := skillhub.LoadFromProject(projectRoot)
	if err != nil {
		return fmt.Errorf("load skillhub config: %w", err)
	}

	action := args[0]
	rest := args[1:]

	switch action {
	case "help", "-h", "--help":
		return printSkillUsage(stdout)
	case "login":
		return runSkillLogin(stdout, stderr, projectRoot, cfg, rest)
	case "logout":
		return runSkillLogout(stdout, projectRoot, cfg)
	case "whoami":
		return runSkillWhoAmI(stdout, cfg.Resolved())
	case "search":
		return runSkillSearch(stdout, cfg.Resolved(), rest)
	case "list":
		return runSkillList(stdout, cfg.Resolved(), rest)
	case "get":
		return runSkillGet(stdout, cfg.Resolved(), rest)
	case "install":
		return runSkillInstall(stdout, projectRoot, cfg.Resolved(), rest)
	case "uninstall":
		return runSkillUninstall(stdout, projectRoot, rest)
	case "publish":
		return runSkillPublish(stdout, cfg.Resolved(), rest)
	case "publish-learned":
		return runSkillPublishLearned(stdout, cfg.Resolved(), rest)
	case "sync":
		return runSkillSync(stdout, projectRoot, cfg.Resolved())
	default:
		return fmt.Errorf("unknown skill action: %s (try 'saker skill help')", action)
	}
}

func printSkillUsage(out io.Writer) error {
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

func runSkillLogin(stdout, stderr io.Writer, projectRoot string, cfg skillhub.Config, args []string) error {
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
		registry = skillhub.DefaultRegistry
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := skillhub.New(registry)
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
	if err := skillhub.SaveToProject(projectRoot, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if who != nil {
		fmt.Fprintf(stdout, "Logged in as %s (%s)\n", who.Handle, who.ID)
	} else {
		fmt.Fprintln(stdout, "Token saved.")
	}
	return nil
}

func runSkillLogout(stdout io.Writer, projectRoot string, cfg skillhub.Config) error {
	if cfg.Token == "" && cfg.Handle == "" {
		fmt.Fprintln(stdout, "No skillhub credentials found.")
		return nil
	}
	cfg.Token = ""
	// keep handle as a hint for next login prompt
	if err := skillhub.SaveToProject(projectRoot, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(stdout, "Skillhub token cleared.")
	return nil
}

func runSkillWhoAmI(stdout io.Writer, cfg skillhub.Config) error {
	client, err := newSkillClient(cfg)
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

func runSkillSearch(stdout io.Writer, cfg skillhub.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill search <query>")
	}
	client, err := newSkillClient(cfg)
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

func runSkillList(stdout io.Writer, cfg skillhub.Config, args []string) error {
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
	client, err := newSkillClient(cfg)
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

func runSkillGet(stdout io.Writer, cfg skillhub.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill get <slug>")
	}
	slug := args[0]
	client, err := newSkillClient(cfg)
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

func runSkillInstall(stdout io.Writer, projectRoot string, cfg skillhub.Config, args []string) error {
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
	client, err := newSkillClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := client.Install(ctx, slug, skillhub.InstallOptions{
		Dir:     skillhub.SubscribedDir(projectRoot),
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

func runSkillUninstall(stdout io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill uninstall <slug>")
	}
	slug := args[0]
	if err := skillhub.Uninstall(skillhub.SubscribedDir(projectRoot), slug); err != nil {
		return err
	}
	// Remove from subscriptions list.
	cfg, _ := skillhub.LoadFromProject(projectRoot)
	cfg.Subscriptions = removeString(cfg.Subscriptions, slug)
	_ = skillhub.SaveToProject(projectRoot, cfg)
	fmt.Fprintf(stdout, "Uninstalled %s\n", slug)
	return nil
}

func runSkillSync(stdout io.Writer, projectRoot string, cfg skillhub.Config) error {
	if len(cfg.Subscriptions) == 0 {
		fmt.Fprintln(stdout, "No subscriptions configured.")
		return nil
	}
	client, err := newSkillClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	root := skillhub.SubscribedDir(projectRoot)
	for _, slug := range cfg.Subscriptions {
		etag := readOriginETag(root, slug)
		res, err := client.Install(ctx, slug, skillhub.InstallOptions{Dir: root, ETag: etag})
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

func runSkillPublish(stdout io.Writer, cfg skillhub.Config, args []string) error {
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
	client, err := newSkillClient(cfg)
	if err != nil {
		return err
	}
	files, err := skillhub.CollectDirFiles(dir, 0)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := client.Publish(ctx, skillhub.PublishRequest{
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

func runSkillPublishLearned(stdout io.Writer, cfg skillhub.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: saker skill publish-learned <dir>")
	}
	if cfg.Handle == "" {
		return errors.New("no handle configured; run 'saker skill login' first")
	}
	client, err := newSkillClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := client.PublishLearned(ctx, args[0], skillhub.PublishLearnedOptions{
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

func newSkillClient(cfg skillhub.Config) (*skillhub.Client, error) {
	if cfg.Offline {
		return nil, errors.New("skillhub is in offline mode (SKILLHUB_OFFLINE=1)")
	}
	if strings.TrimSpace(cfg.Registry) == "" {
		return nil, errors.New("no registry configured; set SKILLHUB_REGISTRY or .saker/settings.json skillhub.registry")
	}
	opts := []skillhub.ClientOption{}
	if cfg.Token != "" {
		opts = append(opts, skillhub.WithToken(cfg.Token))
	}
	return skillhub.New(cfg.Registry, opts...), nil
}

// addSubscription appends slug to cfg.Subscriptions (dedup) and persists.
func addSubscription(projectRoot string, cfg skillhub.Config, slug string) error {
	for _, s := range cfg.Subscriptions {
		if s == slug {
			return nil
		}
	}
	cfg.Subscriptions = append(cfg.Subscriptions, slug)
	return skillhub.SaveToProject(projectRoot, cfg)
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
