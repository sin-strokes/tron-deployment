package cmd

import (
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

// parseLabelFilter accepts the `--label key=value` flag values from list /
// inspect and returns an in-memory representation that callers feed into
// matchesLabels. Each filter must be of the form "key=value"; multiple
// occurrences of the flag AND together (an entry must satisfy every
// filter to pass).
//
// We intentionally don't support the full docker `--filter` minilanguage
// (regex, !=, ranges) — keeps the surface tiny. If callers need more they
// can post-process JSON output through jq.
func parseLabelFilter(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		k, val, ok := strings.Cut(v, "=")
		if !ok || k == "" {
			return nil, output.NewError("VALIDATION_ERROR", output.ExitValidationError,
				fmt.Sprintf("invalid --label %q: expected key=value", v))
		}
		out[k] = val
	}
	return out, nil
}

// matchesLabels reports whether a node carries every k=v in filter.
// Nodes with no labels at all match only the empty filter.
func matchesLabels(n *state.ManagedNode, filter map[string]string) bool {
	for k, v := range filter {
		got, ok := n.Labels[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}
