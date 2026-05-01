package diagnosis

import (
	"context"
	"fmt"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// DiskChecker verifies sufficient disk space on the data directory.
type DiskChecker struct{}

func (c *DiskChecker) Name() string { return "disk_space" }

func (c *DiskChecker) Run(ctx context.Context, tgt target.Target, opts CheckOpts) CheckResult {
	path := "/"
	if opts.InstallPath != "" {
		path = opts.InstallPath
	}

	free, err := tgt.DiskFree(ctx, path)
	if err != nil {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not check disk space",
		}
	}

	freeGB := free / (1024 * 1024 * 1024)

	// Minimum thresholds by network
	warnGB := uint64(50)
	critGB := uint64(20)
	if opts.Network == "mainnet" {
		warnGB = 200
		critGB = 50
	}

	if freeGB < critGB {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusFail,
			Message: fmt.Sprintf("Critical: only %dGB free (<%dGB)", freeGB, critGB),
			Suggestions: []string{
				"Free disk space immediately",
				"Consider pruning old data",
				"Move data to a larger volume",
			},
		}
	}

	if freeGB < warnGB {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%dGB free (<%dGB recommended)", freeGB, warnGB),
			Suggestions: []string{
				"Plan disk expansion",
				"Monitor disk usage trend",
			},
		}
	}

	return CheckResult{
		Name:    c.Name(),
		Status:  StatusPass,
		Message: fmt.Sprintf("%dGB free", freeGB),
	}
}
