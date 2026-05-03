package intent

import (
	"path/filepath"
	"testing"
)

// Benchmarks for hot-path intent operations. These don't enforce a
// regression threshold today — establish a baseline so future
// changes show up in `go test -bench=. -benchmem` diffs.
//
// To capture a baseline:
//
//	go test -bench=. -benchmem -count=5 ./internal/intent/ > /tmp/intent.txt
//
// To compare after a refactor:
//
//	go test -bench=. -benchmem -count=5 ./internal/intent/ > /tmp/intent.new
//	benchstat /tmp/intent.txt /tmp/intent.new

func benchPath(b *testing.B) string {
	b.Helper()
	abs, err := filepath.Abs("../../examples/nile-fullnode.yaml")
	if err != nil {
		b.Fatalf("abs: %v", err)
	}
	return abs
}

func BenchmarkLoad(b *testing.B) {
	p := benchPath(b)
	b.ResetTimer()
	for b.Loop() {
		_, err := Load(p)
		if err != nil {
			b.Fatalf("Load: %v", err)
		}
	}
}

func BenchmarkLoadRaw(b *testing.B) {
	p := benchPath(b)
	b.ResetTimer()
	for b.Loop() {
		_, err := LoadRaw(p)
		if err != nil {
			b.Fatalf("LoadRaw: %v", err)
		}
	}
}

func BenchmarkValidate(b *testing.B) {
	p := benchPath(b)
	parsed, err := LoadRaw(p)
	if err != nil {
		b.Fatalf("LoadRaw: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if err := Validate(parsed); err != nil {
			b.Fatalf("Validate: %v", err)
		}
	}
}
