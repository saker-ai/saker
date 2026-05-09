// Command run_eval runs evaluation suites and reports results.
//
// Usage:
//
//	go run ./eval/cmd/run_eval --suite=all
//	go run ./eval/cmd/run_eval --suite=system_prompt --output=json
//	go run ./eval/cmd/run_eval --suite=all --online --model=claude-haiku-4-5-20251001
//	go run ./eval/cmd/run_eval --suite=performance --bench-time=3s
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// offlineSuites run without an API key.
var offlineSuites = map[string]string{
	"system_prompt":      "./eval/suites/system_prompt/...",
	"tool_registration":  "./eval/suites/tool_registration/...",
	"multi_turn":         "./eval/suites/multi_turn/...",
	"safety_integration": "./eval/suites/safety_integration/...",
	"pipeline":           "./eval/suites/pipeline/...",
	"performance":        "./eval/suites/performance/...",
}

// onlineSuites require ANTHROPIC_API_KEY and use the integration build tag.
var onlineSuites = map[string]string{
	"llm_tool_selection": "./eval/suites/llm_tool_selection/...",
	"llm_multi_turn":     "./eval/suites/llm_multi_turn/...",
	"llm_safety":         "./eval/suites/llm_safety/...",
	"llm_system_prompt":  "./eval/suites/llm_system_prompt/...",
}

var offlineOrder = []string{
	"system_prompt", "tool_registration", "multi_turn",
	"safety_integration", "pipeline", "performance",
}

var onlineOrder = []string{
	"llm_tool_selection", "llm_multi_turn", "llm_safety", "llm_system_prompt",
}

type suiteResult struct {
	Suite    string `json:"suite"`
	Pass     bool   `json:"pass"`
	Online   bool   `json:"online"`
	Output   string `json:"output"`
	Duration string `json:"duration"`
}

func main() {
	suiteFlag := flag.String("suite", "all", "comma-separated suite names or 'all'")
	output := flag.String("output", "text", "output format: text or json")
	benchTime := flag.String("bench-time", "1s", "benchmark duration (only for performance suite)")
	timeout := flag.String("timeout", "300s", "test timeout")
	online := flag.Bool("online", false, "include online LLM eval suites (requires ANTHROPIC_API_KEY)")
	threshold := flag.Float64("threshold", 0.0, "minimum pass rate (0.0-1.0); exit 1 if below")
	flag.Parse()

	suites := resolveSuites(*suiteFlag, *online)
	if len(suites) == 0 {
		fmt.Fprintf(os.Stderr, "no matching suites for %q\n", *suiteFlag)
		os.Exit(1)
	}

	if *online && os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "warning: --online requires ANTHROPIC_API_KEY to be set")
	}

	var results []suiteResult
	allPass := true

	for _, name := range suites {
		pkg, isOnline := lookupSuite(name)
		if pkg == "" {
			fmt.Fprintf(os.Stderr, "unknown suite: %s\n", name)
			os.Exit(1)
		}

		args := []string{"test", "-v", "-timeout", *timeout}
		if isOnline {
			args = append(args, "-tags", "integration")
		}
		args = append(args, pkg)
		if name == "performance" {
			args = append(args, "-bench", ".", "-benchtime", *benchTime)
		}

		start := time.Now()
		cmd := exec.Command("go", args...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)

		pass := err == nil
		if !pass {
			allPass = false
		}

		results = append(results, suiteResult{
			Suite:    name,
			Pass:     pass,
			Online:   isOnline,
			Output:   string(out),
			Duration: dur.Round(time.Millisecond).String(),
		})

		if *output == "text" {
			status := "PASS"
			if !pass {
				status = "FAIL"
			}
			tag := ""
			if isOnline {
				tag = " [online]"
			}
			fmt.Printf("[%s] %s%s (%s)\n", status, name, tag, dur.Round(time.Millisecond))
			if !pass {
				fmt.Println(string(out))
			}
		}
	}

	passed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		}
	}

	if *output == "json" {
		report := struct {
			Timestamp string        `json:"timestamp"`
			AllPass   bool          `json:"all_pass"`
			PassRate  float64       `json:"pass_rate"`
			Suites    []suiteResult `json:"suites"`
		}{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			AllPass:   allPass,
			PassRate:  float64(passed) / float64(len(results)),
			Suites:    results,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		fmt.Println()
		fmt.Printf("Result: %d/%d suites passed", passed, len(results))
		if len(results) > 0 {
			fmt.Printf(" (%.0f%%)", float64(passed)/float64(len(results))*100)
		}
		fmt.Println()
	}

	if *threshold > 0 && len(results) > 0 {
		rate := float64(passed) / float64(len(results))
		if rate < *threshold {
			fmt.Fprintf(os.Stderr, "pass rate %.1f%% below threshold %.1f%%\n", rate*100, *threshold*100)
			os.Exit(1)
		}
	}

	if !allPass {
		os.Exit(1)
	}
}

func lookupSuite(name string) (pkg string, isOnline bool) {
	if p, ok := offlineSuites[name]; ok {
		return p, false
	}
	if p, ok := onlineSuites[name]; ok {
		return p, true
	}
	return "", false
}

func resolveSuites(input string, includeOnline bool) []string {
	if input == "all" {
		out := append([]string{}, offlineOrder...)
		if includeOnline {
			out = append(out, onlineOrder...)
		}
		return out
	}
	var out []string
	for _, s := range strings.Split(input, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
