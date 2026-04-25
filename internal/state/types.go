package state

import "time"

// ManagedNode represents a deployed node tracked in the state file.
type ManagedNode struct {
	Name            string     `json:"name"`
	IntentHash      string     `json:"intent_hash"`
	ConfigHash      string     `json:"config_hash"`
	Version         string     `json:"version"`
	Target          NodeTarget `json:"target"`
	Runtime         string     `json:"runtime"`
	Status          string     `json:"status"` // running, stopped, error, unknown
	LastApplied     time.Time  `json:"last_applied"`
	PreviousVersion string     `json:"previous_version,omitempty"`
	ComposePath     string     `json:"compose_path,omitempty"`
	SystemdUnit     string     `json:"systemd_unit,omitempty"`
	InstallPath     string     `json:"install_path,omitempty"`
	// HTTPPort and GRPCPort capture the API ports as configured at deploy time
	// so probe commands (health, diagnose, verify) can target the right port
	// without re-reading the intent file. Older state files predate these
	// fields — callers must fall back to defaults when zero.
	HTTPPort int `json:"http_port,omitempty"`
	GRPCPort int `json:"grpc_port,omitempty"`
}

// NodeTarget is the target info stored in state (subset of intent.Target).
type NodeTarget struct {
	Type         string `json:"type"`
	Host         string `json:"host,omitempty"`
	User         string `json:"user,omitempty"`
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
}

// DeploymentState is the top-level state file structure.
type DeploymentState struct {
	Version int           `json:"version"`
	Nodes   []ManagedNode `json:"nodes"`
}

// AuditEntry represents a single line in the audit log (JSONL format).
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Command    string    `json:"command"`
	Node       string    `json:"node,omitempty"`
	Target     string    `json:"target"`
	IntentHash string    `json:"intent_hash,omitempty"`
	Result     string    `json:"result"` // success, failure, no_change
	DurationMs int64     `json:"duration_ms"`
	ErrorCode  string    `json:"error_code,omitempty"`
}
