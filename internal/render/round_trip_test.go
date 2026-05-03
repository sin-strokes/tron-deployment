package render

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// TestRenderHOCON_RoundTripFields renders each example intent to
// HOCON and asserts the rendered file actually contains the values
// the intent declared at the canonical typesafe-config paths
// java-tron expects.
//
// Without this test, the HOCON template could silently route an
// intent's `ports.http: 8090` to (say) `node.discovery.port` instead
// of `node.http.fullNodePort`, and we'd only find out when java-tron
// refused to start. The previous render tests only checked the
// rendered text parsed; this one checks that the parsed values are
// the right ones.
//
// We don't pull a full HOCON parser — typesafe-config is JVM-only
// and the closest Go ports drag in too much weight for a test that
// only needs key-presence-and-value asserts. Instead we use small
// regexes against the rendered text. Each regex pins one mapping.
func TestRenderHOCON_RoundTripFields(t *testing.T) {
	cases := []struct {
		intentFile string
		network    string
		// must lists (regex, why) — the regex must match the rendered
		// HOCON. why is shown when the assertion fails.
		must []rendered
	}{
		{
			intentFile: "nile-fullnode.yaml",
			network:    "nile",
			must: []rendered{
				{regexp.MustCompile(`(?m)^\s*listen\.port\s*=\s*18888\b`),
					"intent.ports.p2p (18888) → node.listen.port"},
				{regexp.MustCompile(`(?m)^\s*walletExtensionApi\s*=\s*true\b`),
					"every fullnode renders walletExtensionApi=true"},
			},
		},
		{
			intentFile: "mainnet-fullnode.yaml",
			network:    "mainnet",
			must: []rendered{
				{regexp.MustCompile(`(?m)^\s*listen\.port\s*=\s*18888\b`),
					"intent.ports.p2p (18888) → node.listen.port"},
			},
		},
		{
			intentFile: "private-network.yaml",
			network:    "private",
			must: []rendered{
				{regexp.MustCompile(`(?m)^\s*listen\.port\s*=\s*18888\b`),
					"witness's intent.ports.p2p (18888) → node.listen.port"},
				{regexp.MustCompile(`localwitness = \["[0-9a-fA-F<UNSET].+"\]`),
					"witness_key.private_key_env → localwitness inlined (resolved or <UNSET:...> placeholder)"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.intentFile, func(t *testing.T) {
			path, err := filepath.Abs("../../examples/" + tc.intentFile)
			if err != nil {
				t.Fatalf("abs: %v", err)
			}
			parsed, err := intent.Load(path)
			if err != nil {
				t.Fatalf("intent.Load: %v", err)
			}
			node := &parsed.Nodes[0]
			body, err := RenderHOCON("", parsed, node)
			if err != nil {
				t.Fatalf("RenderHOCON: %v", err)
			}
			if parsed.Network != tc.network {
				t.Errorf("intent network: want %s, got %s", tc.network, parsed.Network)
			}
			for _, r := range tc.must {
				if !r.re.MatchString(body) {
					t.Errorf("rendered HOCON missing %q\nregex: %s\nrendered (first 200 chars):\n%s",
						r.why, r.re, snippet(body, 200))
				}
			}
		})
	}
}

type rendered struct {
	re  *regexp.Regexp
	why string
}

func snippet(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
