package render

import (
	"path/filepath"
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// Benchmarks for HOCON / compose rendering. The render path is the
// hot loop on every `apply` and the `config render` command — order
// of magnitude regressions here translate directly to slower agent
// turnarounds.
//
// To capture a baseline / compare:
//
//	go test -bench=. -benchmem -count=5 ./internal/render/ > /tmp/render.txt

func benchIntent(b *testing.B) (*intent.Intent, *intent.NodeSpec) {
	b.Helper()
	p, err := filepath.Abs("../../examples/nile-fullnode.yaml")
	if err != nil {
		b.Fatalf("abs: %v", err)
	}
	in, err := intent.Load(p)
	if err != nil {
		b.Fatalf("intent.Load: %v", err)
	}
	return in, &in.Nodes[0]
}

func BenchmarkRenderHOCON(b *testing.B) {
	in, node := benchIntent(b)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := RenderHOCON("", in, node); err != nil {
			b.Fatalf("RenderHOCON: %v", err)
		}
	}
}

func BenchmarkRenderCompose(b *testing.B) {
	in, node := benchIntent(b)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = RenderCompose(in.Name, in, node, "", "")
	}
}
