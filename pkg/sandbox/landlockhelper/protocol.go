package landlockhelper

// Request is the stdin protocol payload for Landlock helper mode.
type Request struct {
	Version   string            `json:"version"`
	SessionID string            `json:"session_id"`
	Command   string            `json:"command"`
	GuestCwd  string            `json:"guest_cwd"`
	TimeoutMs int64             `json:"timeout_ms"`
	Env       map[string]string `json:"env,omitempty"`
	ROPaths   []string          `json:"ro_paths,omitempty"`
	RWPaths   []string          `json:"rw_paths,omitempty"`
}

// Response is the stdout protocol payload for Landlock helper mode.
type Response struct {
	Success    bool   `json:"success"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
