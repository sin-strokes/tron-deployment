package runtime

import (
	"context"
	"io"
)

// LogOpts configures log retrieval.
type LogOpts struct {
	Tail   int
	Follow bool
}

// NodeStatus represents the current status of a deployed node.
type NodeStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // running, stopped, error, unknown
	Uptime  string `json:"uptime,omitempty"`
	Version string `json:"version,omitempty"`
}

// Runtime abstracts the deployment runtime (Docker or Jar+systemd).
type Runtime interface {
	// Deploy deploys the node to the target.
	Deploy(ctx context.Context, opts DeployOpts) error

	// Start starts a previously stopped node.
	Start(ctx context.Context, name string) error

	// Stop stops a running node.
	Stop(ctx context.Context, name string) error

	// Remove removes a deployed node. If purge is true, also removes data.
	Remove(ctx context.Context, name string, purge bool) error

	// Status returns the current node status.
	Status(ctx context.Context, name string) (*NodeStatus, error)

	// Logs returns a reader for node logs.
	Logs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error)
}

// DeployOpts contains everything needed for a deployment.
type DeployOpts struct {
	Name        string
	ConfigData  []byte
	ComposeData []byte // Docker runtime only
	SystemdData []byte // Jar runtime only
	JarPath     string // Jar runtime only
	JarURL      string // Jar runtime only
	JarSHA256   string // Jar runtime only
	EnvVars     map[string]string
}
