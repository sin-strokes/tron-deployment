package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestResources_StaticListAndRead exercises the three fixed-URI
// resources: state, audit-log, schema-manifest.
func TestResources_StaticListAndRead(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	got := map[string]bool{}
	for _, r := range res.Resources {
		got[r.URI] = true
	}
	want := []string{
		"trond://state",
		"trond://audit-log",
		"trond://schema-manifest",
	}
	for _, uri := range want {
		if !got[uri] {
			t.Errorf("resource %q missing from ListResources", uri)
		}
	}

	for _, uri := range want {
		t.Run(uri, func(t *testing.T) {
			out, err := session.ReadResource(context.Background(),
				&mcpsdk.ReadResourceParams{URI: uri})
			if err != nil {
				t.Fatalf("ReadResource(%s): %v", uri, err)
			}
			if len(out.Contents) == 0 {
				t.Fatalf("ReadResource(%s) returned no contents", uri)
			}
			body := out.Contents[0]
			switch uri {
			case "trond://state", "trond://schema-manifest":
				var v any
				if err := json.Unmarshal([]byte(body.Text), &v); err != nil {
					t.Errorf("%s body not JSON: %v\n%s", uri, err, body.Text)
				}
			case "trond://audit-log":
				for _, line := range strings.Split(body.Text, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var v any
					if err := json.Unmarshal([]byte(line), &v); err != nil {
						t.Errorf("%s: line is not JSON: %v\nline: %s",
							uri, err, line)
					}
				}
			}
		})
	}
}

// TestResources_TemplatesListed verifies that ListResourceTemplates
// surfaces the per-node URI templates we registered.
func TestResources_TemplatesListed(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	got := map[string]bool{}
	for _, rt := range res.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	for _, want := range []string{
		"trond://nodes/{name}/endpoints",
		"trond://nodes/{name}/conf",
	} {
		if !got[want] {
			t.Errorf("template %q missing", want)
		}
	}
}

// TestNodeNameFromURI pins the URI parser used by both per-node
// resource templates. Bad shapes must fail loud rather than read
// the wrong node.
func TestNodeNameFromURI(t *testing.T) {
	cases := []struct {
		uri     string
		suffix  string
		want    string
		wantErr bool
	}{
		{"trond://nodes/my-fullnode/endpoints", "/endpoints", "my-fullnode", false},
		{"trond://nodes/my-fullnode/conf", "/conf", "my-fullnode", false},
		{"trond://nodes//endpoints", "/endpoints", "", true},
		{"trond://nodes/a/b/endpoints", "/endpoints", "", true},
		{"trond://nodes/x?y/endpoints", "/endpoints", "", true},
		{"trond://state", "/endpoints", "", true},
	}
	for _, tc := range cases {
		got, err := nodeNameFromURI(tc.uri, tc.suffix)
		if (err != nil) != tc.wantErr {
			t.Errorf("uri=%q: err=%v, wantErr=%v", tc.uri, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("uri=%q: got %q, want %q", tc.uri, got, tc.want)
		}
	}
}
