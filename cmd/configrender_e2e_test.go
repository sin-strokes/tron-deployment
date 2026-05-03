//go:build e2e

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestE2E_ConfigRender_Parseable runs `trond config render
// --output-dir <tmp>` for a representative intent and asserts:
//
//   - the rendered docker-compose.yaml parses as YAML AND survives
//     `docker compose config --quiet` (Docker-gated — that's the
//     real consumer path)
//   - the rendered HOCON .conf file parses as HOCON (a structurally
//     valid config; we don't run java-tron against it, just the
//     parser)
//
// Why this matters: `config render` is the staging step that all
// `apply` calls write through. If the rendered files don't parse
// for the consumer, the apply silently fails much later when docker
// compose tries to consume them — far from the rendering bug.
func TestE2E_ConfigRender_Parseable(t *testing.T) {
	stateDir, env := e2eEnv(t)
	intentPath := absExample(t, "examples/nile-fullnode.yaml")
	outDir := filepath.Join(stateDir, "render-out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --output-dir writes files to disk regardless of --output text/json.
	// (Until recently, combining --output-dir with --output json silently
	// wrote nothing to disk; that's now fixed and verified by this
	// test using --output json explicitly.)
	runTrondCtx(ctx, t, env, "config", "render", intentPath,
		"--output-dir", outDir, "--output", "json")

	composePath := filepath.Join(outDir, "docker-compose.yaml")
	confPath := filepath.Join(outDir, "nile-fullnode.conf")

	// 1. compose YAML parses as YAML.
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	var composeDoc map[string]any
	if err := yaml.Unmarshal(composeData, &composeDoc); err != nil {
		t.Fatalf("compose YAML did not parse: %v\nbody:\n%s", err, composeData)
	}
	if _, ok := composeDoc["services"]; !ok {
		t.Errorf("compose missing top-level `services` key:\n%s", composeData)
	}

	// 2. compose YAML survives `docker compose config --quiet`.
	//    This catches version-specific shape errors that vanilla YAML
	//    parsing misses (unsupported `depends_on:condition`, network
	//    name collisions, etc.). Docker-gated.
	t.Run("docker-compose-config", func(t *testing.T) {
		skipUnlessDocker(t)
		dctx, dcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dcancel()
		cmd := exec.CommandContext(dctx, "docker", "compose",
			"-f", composePath, "config", "--quiet")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose config rejected the rendered file: %v\noutput:\n%s", err, out)
		}
	})

	// 3. HOCON .conf parses as HOCON. We use a structurally lenient
	//    parser that mirrors java-tron's typesafe-config: any field
	//    can appear, but braces / quotes / arrays must be balanced.
	hoconData, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read hocon: %v", err)
	}
	if err := lightweightHOCONParse(string(hoconData)); err != nil {
		t.Fatalf("rendered HOCON did not parse: %v\nbody snippet:\n%s",
			err, snippet(string(hoconData), 800))
	}
}

// lightweightHOCONParse is a minimal structural parser for the
// rendered .conf file. We don't pull a full HOCON library because the
// repo doesn't currently depend on one; what java-tron needs is
// balanced braces / brackets / quotes and at least one root-level
// key, which is a weaker guarantee than full HOCON validity but
// catches every realistic rendering bug we've seen (mismatched
// braces from string interpolation, premature EOF mid-block, etc.).
func lightweightHOCONParse(s string) error {
	depthBrace := 0
	depthBracket := 0
	inString := false
	rootKeys := 0
	prevWS := true
	for i, r := range s {
		switch {
		case r == '"' && !escaped(s, i):
			inString = !inString
		case inString:
			// skip inside string
		case r == '{':
			depthBrace++
		case r == '}':
			depthBrace--
			if depthBrace < 0 {
				return errAtPos(s, i, "unmatched '}'")
			}
		case r == '[':
			depthBracket++
		case r == ']':
			depthBracket--
			if depthBracket < 0 {
				return errAtPos(s, i, "unmatched ']'")
			}
		case r == '#':
			// line comment — skip to newline
			for i < len(s) && s[i] != '\n' {
				i++
			}
		}
		if depthBrace == 0 && depthBracket == 0 && !inString && prevWS && (r >= 'a' && r <= 'z') {
			rootKeys++
		}
		prevWS = r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}
	if inString {
		return errString("unterminated quoted string")
	}
	if depthBrace != 0 {
		return errString("unbalanced { } at EOF")
	}
	if depthBracket != 0 {
		return errString("unbalanced [ ] at EOF")
	}
	if rootKeys == 0 {
		return errString("no root-level keys")
	}
	return nil
}

func escaped(s string, i int) bool {
	if i == 0 {
		return false
	}
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 1
}

func errAtPos(s string, i int, what string) error {
	return errString(what + " at offset " + itoa(i) + " near " + snippet(s[i:], 60))
}

func errString(s string) error { return &simpleErr{s} }

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func snippet(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
