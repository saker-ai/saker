package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/eval/terminalbench"
	"github.com/cinience/saker/pkg/sandbox/dockerenv"
)

func TestRunEvalCommand_NoArgs_PrintsUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := runEvalCommand(&stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("want missing subcommand error, got %v", err)
	}
	if !strings.Contains(stderr.String(), "terminalbench") {
		t.Fatalf("usage should mention terminalbench, got %q", stderr.String())
	}
}

func TestRunEvalCommand_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := runEvalCommand(&stdout, &stderr, []string{"swe-bench"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("want unknown subcommand error, got %v", err)
	}
}

func TestRunEvalCommand_HelpVariants(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if err := runEvalCommand(&stdout, &stderr, []string{arg}); err != nil {
			t.Fatalf("%s: unexpected error %v", arg, err)
		}
		if !strings.Contains(stdout.String(), "subcommands") {
			t.Fatalf("%s: usage missing 'subcommands' section, got %q", arg, stdout.String())
		}
	}
}

func TestRunEvalTerminalBench_RequiresDataset(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := runEvalTerminalBench(&stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--dataset is required") {
		t.Fatalf("want --dataset error, got %v", err)
	}
}

func TestParsePullPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw    string
		want   dockerenv.PullPolicy
		hasErr bool
	}{
		{"", dockerenv.PullIfMissing, false},
		{"always", dockerenv.PullAlways, false},
		{"if-missing", dockerenv.PullIfMissing, false},
		{"missing", dockerenv.PullIfMissing, false},
		{"never", dockerenv.PullNever, false},
		{"none", dockerenv.PullNever, false},
		{"ALWAYS", dockerenv.PullAlways, false},
		{"sometimes", "", true},
	}
	for _, tc := range cases {
		got, err := parsePullPolicy(tc.raw)
		if tc.hasErr {
			if err == nil {
				t.Errorf("parsePullPolicy(%q) want error, got %v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePullPolicy(%q) error: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parsePullPolicy(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestEvalMultiValue_AppendsAndSplits(t *testing.T) {
	t.Parallel()
	var v evalMultiValue
	if err := v.Set("foo"); err != nil {
		t.Fatalf("Set foo: %v", err)
	}
	if err := v.Set("bar,baz, qux"); err != nil {
		t.Fatalf("Set bar,baz,qux: %v", err)
	}
	want := []string{"foo", "bar", "baz", "qux"}
	if len(v) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(v), len(want), []string(v))
	}
	for i, w := range want {
		if v[i] != w {
			t.Errorf("v[%d] = %q, want %q", i, v[i], w)
		}
	}
	if got := v.String(); got != "foo,bar,baz,qux" {
		t.Errorf("String() = %q", got)
	}
}

func TestBuildMirrorEnv_DefaultIsBare(t *testing.T) {
	t.Parallel()
	got, err := buildMirrorEnv(false, nil)
	if err != nil {
		t.Fatalf("buildMirrorEnv: %v", err)
	}
	if got != nil {
		t.Fatalf("default should be nil (bare saker baseline), got %v", got)
	}
}

func TestBuildMirrorEnv_WithMirrorSeedsDefaults(t *testing.T) {
	t.Parallel()
	got, err := buildMirrorEnv(true, nil)
	if err != nil {
		t.Fatalf("buildMirrorEnv: %v", err)
	}
	for k, v := range terminalbench.DefaultMirrorEnv {
		if got[k] != v {
			t.Errorf("default %s = %q, want %q", k, got[k], v)
		}
	}
	// Independent copy: mutating result must not poison DefaultMirrorEnv.
	got["PIP_INDEX_URL"] = "tampered"
	if terminalbench.DefaultMirrorEnv["PIP_INDEX_URL"] == "tampered" {
		t.Fatal("buildMirrorEnv leaked default map by reference")
	}
}

func TestBuildMirrorEnv_WithMirrorOverrideAndDelete(t *testing.T) {
	t.Parallel()
	got, err := buildMirrorEnv(true, []string{
		"PIP_INDEX_URL=https://internal.mirror/simple/", // override
		"HF_ENDPOINT=",   // delete default
		"NEW_KEY=newval", // add new
	})
	if err != nil {
		t.Fatalf("buildMirrorEnv: %v", err)
	}
	if got["PIP_INDEX_URL"] != "https://internal.mirror/simple/" {
		t.Errorf("override not applied: %q", got["PIP_INDEX_URL"])
	}
	if _, ok := got["HF_ENDPOINT"]; ok {
		t.Errorf("HF_ENDPOINT should be deleted, got %q", got["HF_ENDPOINT"])
	}
	if got["NEW_KEY"] != "newval" {
		t.Errorf("new key not added: %q", got["NEW_KEY"])
	}
}

func TestBuildMirrorEnv_OverridesOnlyWithoutWithMirror(t *testing.T) {
	t.Parallel()
	got, err := buildMirrorEnv(false, []string{"CUSTOM=val"})
	if err != nil {
		t.Fatalf("buildMirrorEnv: %v", err)
	}
	if got["CUSTOM"] != "val" {
		t.Errorf("custom override not applied: %q", got["CUSTOM"])
	}
	for k := range terminalbench.DefaultMirrorEnv {
		if _, ok := got[k]; ok {
			t.Errorf("default %s leaked without --with-mirror", k)
		}
	}
}

func TestBuildMirrorEnv_Errors(t *testing.T) {
	t.Parallel()
	if _, err := buildMirrorEnv(false, []string{"DROP="}); err == nil {
		t.Fatal("KEY= delete form must require --with-mirror")
	}
	for _, bad := range []string{"NOEQ", "=novalue"} {
		if _, err := buildMirrorEnv(true, []string{bad}); err == nil {
			t.Errorf("expected error for malformed --mirror %q", bad)
		}
	}
}
