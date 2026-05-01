package diagnosis

import (
	"context"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// CheckStatus represents the result of a diagnostic check.
type CheckStatus string

const (
	StatusPass    CheckStatus = "pass"
	StatusWarning CheckStatus = "warning"
	StatusFail    CheckStatus = "fail"
)

// CheckResult holds the result of a single diagnostic check.
type CheckResult struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Message     string      `json:"message"`
	Suggestions []string    `json:"suggestions,omitempty"`
}

// Checker runs a diagnostic check against a node.
type Checker interface {
	Name() string
	Run(ctx context.Context, tgt target.Target, opts CheckOpts) CheckResult
}

// CheckOpts provides context for the check.
type CheckOpts struct {
	NodeName    string
	NodeType    string // fullnode, witness, solidity, lite
	Network     string // mainnet, nile, private
	Runtime     string // docker, jar
	HTTPPort    int
	GRPCPort    int
	InstallPath string
}

// OverallStatus computes the worst status from a set of results.
func OverallStatus(results []CheckResult) CheckStatus {
	worst := StatusPass
	for _, r := range results {
		if r.Status == StatusFail {
			return StatusFail
		}
		if r.Status == StatusWarning {
			worst = StatusWarning
		}
	}
	return worst
}

// AllCheckers returns all built-in diagnostic checkers.
func AllCheckers() []Checker {
	return []Checker{
		&SyncChecker{},
		&PeersChecker{},
		&DiskChecker{},
		&PortsChecker{},
		&VersionChecker{},
	}
}
