//go:build e2e

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestE2E_MCP_StdioRoundTrip launches the real `trond mcp` binary as
// a subprocess and drives it with an in-process MCP client over its
// stdin/stdout pipes. This is the highest-fidelity test we can write
// of the MCP server: every JSON-RPC byte goes through the same wire
// path that Claude Desktop / Cursor / Cline see.
//
// Coverage:
//   - subprocess starts cleanly with the right cobra subcommand
//   - server.Initialize handshake succeeds
//   - tools/list reports the expected tool names (regression guard
//     for tool registration drift)
//   - a representative read-only tool call (version) round-trips
//
// No Docker required — every tool exercised here is in-process.
func TestE2E_MCP_StdioRoundTrip(t *testing.T) {
	bin := e2eBinary(t)
	_, env := e2eEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	// Drain stderr so a slow / chatty server can't block on a full
	// pipe. We don't assert on it — the protocol traffic lives on
	// stdout — but the bytes still need somewhere to go.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp subprocess: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "trond-e2e-client",
		Version: "test",
	}, nil)

	// IOTransport wires the subprocess pipes into the SDK's
	// newline-delimited JSON-RPC framer. From here on we use the
	// high-level client API the same way Claude Desktop would.
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{
		Reader: stdout,
		Writer: nopWriteCloser{stdin},
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect (initialize handshake): %v", err)
	}
	defer session.Close()

	// 1. tools/list — every registered tool must show up.
	listRes, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range listRes.Tools {
		got[tool.Name] = true
	}
	want := []string{
		"list", "status", "inspect",
		"doctor", "version", "health", "diagnose",
		"config_validate", "config_render", "plan", "apply",
		"snapshot_sources", "snapshot_list", "snapshot_jobs", "snapshot_download",
		"knowledge_list", "knowledge_get",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("tool %q missing from subprocess ListTools", w)
		}
	}

	// 2. version round-trip — proves request/response framing works
	//    end-to-end for a tool that returns structured JSON.
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "version"})
	if err != nil {
		t.Fatalf("CallTool version: %v", err)
	}
	if res.IsError {
		t.Fatalf("version returned IsError: %+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("version returned no content")
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &v); err != nil {
		t.Fatalf("version body not JSON: %v\n%s", err, tc.Text)
	}
	for _, k := range []string{"version", "commit", "build_time", "go_version", "platform"} {
		if _, ok := v[k]; !ok {
			t.Errorf("version response missing field %q; got %v", k, v)
		}
	}

	// 3. status of a non-existent node — proves the error-envelope
	//    framing also survives the subprocess boundary.
	res, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "status",
		Arguments: json.RawMessage(`{"name":"definitely-not-deployed"}`),
	})
	if err != nil {
		t.Fatalf("CallTool status: %v", err)
	}
	if !res.IsError {
		t.Fatal("status of missing node should set IsError=true")
	}
	tc, ok = res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent on error, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "NODE_NOT_FOUND") {
		t.Errorf("expected NODE_NOT_FOUND in error envelope, got: %s", tc.Text)
	}
}

// nopWriteCloser adapts a plain io.Writer to io.WriteCloser. We use
// it so closing the SDK transport doesn't close the subprocess's
// stdin out from under it — t.Cleanup handles that explicitly.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
