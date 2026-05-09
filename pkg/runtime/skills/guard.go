package skills

import (
	"fmt"
	"regexp"
	"strings"
)

// GuardVerdict is the outcome of a security scan.
type GuardVerdict string

const (
	GuardSafe      GuardVerdict = "safe"
	GuardCaution   GuardVerdict = "caution"
	GuardDangerous GuardVerdict = "dangerous"
)

// GuardFinding describes a single security concern detected in skill content.
type GuardFinding struct {
	Category string
	Pattern  string
	Severity GuardVerdict
	Line     int
	Snippet  string
}

// GuardResult captures the full scan outcome.
type GuardResult struct {
	Verdict  GuardVerdict
	Findings []GuardFinding
}

// SkillGuard scans skill content for dangerous patterns.
type SkillGuard struct {
	rules []guardRule
}

type guardRule struct {
	category string
	severity GuardVerdict
	pattern  *regexp.Regexp
	desc     string
}

// NewSkillGuard creates a guard with the default rule set.
func NewSkillGuard() *SkillGuard {
	g := &SkillGuard{}
	g.rules = defaultGuardRules()
	return g
}

// Scan checks skill content for security issues.
func (g *SkillGuard) Scan(content string) GuardResult {
	if g == nil || len(g.rules) == 0 {
		return GuardResult{Verdict: GuardSafe}
	}

	lines := strings.Split(content, "\n")
	var findings []GuardFinding
	worst := GuardSafe

	for lineNum, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, rule := range g.rules {
			if rule.pattern.MatchString(line) {
				snippet := trimmed
				if len(snippet) > 120 {
					snippet = snippet[:117] + "..."
				}
				findings = append(findings, GuardFinding{
					Category: rule.category,
					Pattern:  rule.desc,
					Severity: rule.severity,
					Line:     lineNum + 1,
					Snippet:  snippet,
				})
				if rule.severity == GuardDangerous {
					worst = GuardDangerous
				} else if rule.severity == GuardCaution && worst == GuardSafe {
					worst = GuardCaution
				}
			}
		}
	}

	return GuardResult{Verdict: worst, Findings: findings}
}

// IsSafe returns true when no dangerous patterns were found.
func (r GuardResult) IsSafe() bool {
	return r.Verdict != GuardDangerous
}

// Summary returns a human-readable summary of findings.
func (r GuardResult) Summary() string {
	if len(r.Findings) == 0 {
		return "no issues found"
	}
	var parts []string
	for _, f := range r.Findings {
		parts = append(parts, fmt.Sprintf("line %d: [%s] %s — %s", f.Line, f.Severity, f.Category, f.Pattern))
	}
	return strings.Join(parts, "; ")
}

func defaultGuardRules() []guardRule {
	rules := []guardRule{
		// Destructive operations
		r("destructive", GuardDangerous, `rm\s+-[a-zA-Z]*r[a-zA-Z]*f|rm\s+-[a-zA-Z]*f[a-zA-Z]*r`, "recursive force delete"),
		r("destructive", GuardDangerous, `mkfs\b`, "filesystem format"),
		r("destructive", GuardDangerous, `dd\s+.*of=/dev/`, "raw disk overwrite"),
		r("destructive", GuardDangerous, `>\s*/dev/sd[a-z]`, "redirect to block device"),
		r("destructive", GuardCaution, `shutdown|reboot|poweroff`, "system power control"),

		// Exfiltration
		r("exfiltration", GuardDangerous, `curl\b.*\$\{?\w*(KEY|TOKEN|SECRET|PASS)`, "curl with secrets"),
		r("exfiltration", GuardDangerous, `wget\b.*\$\{?\w*(KEY|TOKEN|SECRET|PASS)`, "wget with secrets"),
		r("exfiltration", GuardDangerous, `curl\b.*\bos\.environ\b`, "curl with env vars"),
		r("exfiltration", GuardCaution, `curl\b.*-d\b.*@`, "curl posting file contents"),
		r("exfiltration", GuardCaution, `cat\s+~/\.ssh/`, "reading SSH keys"),
		r("exfiltration", GuardCaution, `cat\s+~/\.aws/`, "reading AWS credentials"),

		// Prompt injection
		r("injection", GuardDangerous, `(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions`, "prompt injection — ignore instructions"),
		r("injection", GuardDangerous, `(?i)you\s+are\s+now\s+(DAN|an?\s+unrestricted)`, "prompt injection — role hijacking"),
		r("injection", GuardDangerous, `(?i)system\s*:\s*you\s+are`, "prompt injection — fake system message"),
		r("injection", GuardCaution, `(?i)disregard\s+(the\s+)?(system|safety)\s+(prompt|rules|guidelines)`, "prompt injection — safety bypass"),

		// Persistence
		r("persistence", GuardDangerous, `crontab\s+-`, "cron job manipulation"),
		r("persistence", GuardCaution, `echo\s+.*>>\s*~/\.\w*rc\b`, "shell RC file modification"),
		r("persistence", GuardCaution, `echo\s+.*>>\s*~/\.ssh/authorized_keys`, "SSH authorized_keys write"),
		r("persistence", GuardCaution, `systemctl\s+(enable|start)`, "systemd service activation"),

		// Network / reverse shell
		r("network", GuardDangerous, `\b(nc|ncat|netcat)\s+-[a-zA-Z]*l`, "netcat listener (reverse shell)"),
		r("network", GuardDangerous, `/dev/tcp/`, "bash TCP redirect"),
		r("network", GuardDangerous, `\bmkfifo\b.*\bnc\b`, "named pipe + netcat"),
		r("network", GuardCaution, `ngrok|serveo\.net|localtunnel`, "tunnel service"),

		// Obfuscation
		r("obfuscation", GuardDangerous, `base64\s+(-d|--decode)\s*\|`, "base64 decode piped to execution"),
		r("obfuscation", GuardDangerous, `\beval\b.*\$\(`, "eval with command substitution"),
		r("obfuscation", GuardCaution, `python.*-c\s*['"]\s*exec\(`, "python exec from string"),

		// Supply chain
		r("supply-chain", GuardCaution, `curl\b.*\|\s*(ba)?sh`, "curl pipe to shell"),
		r("supply-chain", GuardCaution, `wget\b.*\|\s*(ba)?sh`, "wget pipe to shell"),

		// Privilege escalation
		r("privilege", GuardDangerous, `\bsudo\b`, "sudo usage"),
		r("privilege", GuardDangerous, `chmod\s+[0-7]*[4-7][0-7]{2}\s`, "SUID/SGID permission"),
		r("privilege", GuardCaution, `NOPASSWD`, "passwordless sudo"),

		// Credential exposure
		r("credentials", GuardCaution, `(?i)(sk-ant-|sk-proj-|AKIA[0-9A-Z]{16}|ghp_[a-zA-Z0-9]{36}|xoxb-)`, "hardcoded API key/token"),
	}
	return rules
}

func r(category string, severity GuardVerdict, pattern, desc string) guardRule {
	return guardRule{
		category: category,
		severity: severity,
		pattern:  regexp.MustCompile(pattern),
		desc:     desc,
	}
}
