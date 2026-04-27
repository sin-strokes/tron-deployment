package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPick_DefaultsToFirstMatch(t *testing.T) {
	s := Pick(Filter{Network: NetworkMainnet, DBKind: DBKindLite})
	if s == nil || s.Domain != "34.143.247.77" {
		t.Fatalf("expected mainnet lite to default to SG mirror, got %+v", s)
	}
}

func TestPick_RegionNarrowing(t *testing.T) {
	s := Pick(Filter{Network: NetworkMainnet, DBKind: DBKindFull, Region: RegionAmerica})
	if s == nil {
		t.Fatal("expected a US mirror for full mainnet")
	}
	if s.Region != RegionAmerica {
		t.Fatalf("expected region america, got %s", s.Region)
	}
}

func TestPick_NoMatch(t *testing.T) {
	if got := Pick(Filter{Network: "private"}); got != nil {
		t.Fatalf("private network has no mirrors; got %+v", got)
	}
}

func TestLookupDomain(t *testing.T) {
	if s := LookupDomain("database.nileex.io"); s == nil || s.Network != NetworkNile {
		t.Fatalf("nile lookup failed: %+v", s)
	}
	if s := LookupDomain("not-a-real-host"); s != nil {
		t.Fatalf("expected nil for unknown domain, got %+v", s)
	}
}

func TestTarballURL_Variants(t *testing.T) {
	main := *LookupDomain("34.143.247.77")
	if got := TarballURL(main, "backup20250115", DBKindLite); got != "http://34.143.247.77/backup20250115/LiteFullNode_output-directory.tgz" {
		t.Fatalf("mainnet lite URL wrong: %s", got)
	}
	nile := *LookupDomain("database.nileex.io")
	if got := TarballURL(nile, "backup20250115", DBKindLite); got != "https://nile-snapshots.s3-accelerate.amazonaws.com/backup20250115/LiteFullNode_output-directory.tgz" {
		t.Fatalf("nile URL wrong: %s", got)
	}
	if got := TarballURL(main, "x", DBKind("bogus")); got != "" {
		t.Fatalf("unknown kind should return empty, got %s", got)
	}
}

func TestExtractTar_StreamingHonoursForce(t *testing.T) {
	dest := t.TempDir()
	// Pre-place a file we should refuse to clobber without --force.
	clash := filepath.Join(dest, "output-directory", "database", "CURRENT")
	if err := os.MkdirAll(filepath.Dir(clash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clash, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	tgz := buildTGZ(t, map[string]string{
		"output-directory/database/CURRENT": "fresh",
	})

	// force=false → must error on the clobber.
	_, err := extractTar(mustGunzip(t, tgz), dest, false)
	if err == nil {
		t.Fatal("expected error when overwriting without force")
	}

	// force=true → succeeds, content updates.
	_, err = extractTar(mustGunzip(t, tgz), dest, true)
	if err != nil {
		t.Fatalf("force extract: %v", err)
	}
	got, err := os.ReadFile(clash)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fresh" {
		t.Fatalf("file not overwritten: %q", got)
	}
}

func TestExtractTar_RejectsTraversal(t *testing.T) {
	tgz := buildTGZ(t, map[string]string{
		"../escape.txt": "nope",
	})
	dest := t.TempDir()
	_, err := extractTar(mustGunzip(t, tgz), dest, true)
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestDownload_RefusesExistingDatabaseWithoutForce(t *testing.T) {
	dest := t.TempDir()
	// Seed a non-empty database directory.
	dbPath := filepath.Join(dest, "output-directory", "database")
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbPath, "MANIFEST"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := startMirror(t)
	defer srv.Close()

	src := Source{
		Network:       NetworkMainnet,
		DBKind:        DBKindLite,
		DBEngine:      EngineLevelDB,
		Region:        RegionSingapore,
		Domain:        "test",
		BaseURL:       srv.URL,
		IndexStrategy: "html",
	}
	_, err := Download(context.Background(), DownloadOptions{
		Source:  src,
		Backup:  "backup20250101",
		Kind:    DBKindLite,
		DestDir: dest,
		Force:   false,
	})
	if err == nil {
		t.Fatal("expected refusal with existing database")
	}
	var ow *OverwriteError
	if !errorsAs(err, &ow) {
		t.Fatalf("expected OverwriteError, got %T: %v", err, err)
	}
}

// startMirror serves a tiny tarball at /backup20250101/LiteFullNode_output-directory.tgz
// so the disk-space + overwrite checks can exercise without hitting the
// real upstream. Returns an httptest server.
func startMirror(t *testing.T) *httptest.Server {
	t.Helper()
	tgz := buildTGZ(t, map[string]string{"output-directory/database/CURRENT": "ok"})

	mux := http.NewServeMux()
	mux.HandleFunc("/backup20250101/LiteFullNode_output-directory.tgz",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "0")
			w.Write(tgz)
		})
	mux.HandleFunc("/backup20250101/LiteFullNode_output-directory.tgz.md5sum",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	return httptest.NewServer(mux)
}

func buildTGZ(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name: name,
			Size: int64(len(body)),
			Mode: 0o644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustGunzip(t *testing.T, tgz []byte) *gzip.Reader {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// errorsAs is a tiny shim so the test file doesn't need to import
// errors just for one call (and so we can keep the same control flow if
// the typed-error story changes later).
func errorsAs(err error, target any) bool {
	type unwrapper interface{ Unwrap() error }
	for cur := err; cur != nil; {
		if t, ok := target.(**OverwriteError); ok {
			if ow, ok := cur.(*OverwriteError); ok {
				*t = ow
				return true
			}
		}
		u, ok := cur.(unwrapper)
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	return false
}
