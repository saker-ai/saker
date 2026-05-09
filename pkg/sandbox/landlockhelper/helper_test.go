package landlockhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolRoundTrip(t *testing.T) {
	req := Request{
		Version:   "v1",
		SessionID: "test-session",
		Command:   "echo hello",
		GuestCwd:  "/tmp",
		TimeoutMs: 5000,
		ROPaths:   []string{"/usr"},
		RWPaths:   []string{"/tmp"},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Command != req.Command {
		t.Errorf("command: got %q, want %q", decoded.Command, req.Command)
	}
	if decoded.SessionID != req.SessionID {
		t.Errorf("session_id: got %q, want %q", decoded.SessionID, req.SessionID)
	}
	if len(decoded.ROPaths) != 1 || decoded.ROPaths[0] != "/usr" {
		t.Errorf("ro_paths: got %v, want [/usr]", decoded.ROPaths)
	}
	if len(decoded.RWPaths) != 1 || decoded.RWPaths[0] != "/tmp" {
		t.Errorf("rw_paths: got %v, want [/tmp]", decoded.RWPaths)
	}
}

func TestExecuteRequest_Echo(t *testing.T) {
	resp := ExecuteRequest(context.Background(), Request{
		Command:   "echo hello",
		TimeoutMs: 5000,
	})
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "hello" {
		t.Errorf("stdout: got %q, want %q", got, "hello")
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit_code: got %d, want 0", resp.ExitCode)
	}
}

func TestExecuteRequest_NonZeroExit(t *testing.T) {
	resp := ExecuteRequest(context.Background(), Request{
		Command:   "exit 42",
		TimeoutMs: 5000,
	})
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.ExitCode != 42 {
		t.Errorf("exit_code: got %d, want 42", resp.ExitCode)
	}
}

func TestExecuteRequest_Timeout(t *testing.T) {
	resp := ExecuteRequest(context.Background(), Request{
		Command:   "sleep 60",
		TimeoutMs: 100,
	})
	if resp.Success {
		t.Fatal("expected timeout failure")
	}
	if resp.ExitCode != -1 {
		t.Errorf("exit_code: got %d, want -1", resp.ExitCode)
	}
}

func TestExecuteRequest_WorkingDir(t *testing.T) {
	resp := ExecuteRequest(context.Background(), Request{
		Command:   "pwd",
		GuestCwd:  "/tmp",
		TimeoutMs: 5000,
	})
	if !resp.Success {
		t.Fatalf("expected success: %s", resp.Error)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "/tmp" {
		t.Errorf("stdout: got %q, want /tmp", got)
	}
}

func TestRun_StdinStdout(t *testing.T) {
	req := Request{
		Command:   "echo roundtrip",
		TimeoutMs: 5000,
	}
	payload, _ := json.Marshal(req)
	var out bytes.Buffer
	if err := Run(context.Background(), bytes.NewReader(payload), &out, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp Response
	if err := json.NewDecoder(&out).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "roundtrip" {
		t.Errorf("stdout: got %q, want %q", got, "roundtrip")
	}
}

func TestInvoke_InProcess(t *testing.T) {
	// Test binary (.test suffix) triggers in-process execution.
	resp, err := Invoke(context.Background(), Request{
		Command:   "echo invoke-test",
		TimeoutMs: 5000,
	}, "")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "invoke-test" {
		t.Errorf("stdout: got %q, want %q", got, "invoke-test")
	}
}
