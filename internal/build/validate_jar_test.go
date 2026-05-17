package build

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeJAR writes a minimal JAR with the given Main-Class header (or no
// manifest at all if mainClass=="").
func makeJAR(t *testing.T, mainClass string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.jar")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if mainClass != "OMIT-MANIFEST" {
		w, err := zw.Create("META-INF/MANIFEST.MF")
		if err != nil {
			t.Fatalf("create manifest: %v", err)
		}
		_, _ = w.Write([]byte("Manifest-Version: 1.0\n"))
		if mainClass != "" {
			_, _ = w.Write([]byte("Main-Class: " + mainClass + "\n"))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	return path
}

func TestValidateJARMainClass_Match(t *testing.T) {
	path := makeJAR(t, "org.tron.program.FullNode")
	if err := ValidateJARMainClass(path, "org.tron.program.FullNode"); err != nil {
		t.Errorf("expected pass; got %v", err)
	}
}

func TestValidateJARMainClass_Mismatch(t *testing.T) {
	path := makeJAR(t, "com.example.WrongMain")
	err := ValidateJARMainClass(path, "org.tron.program.FullNode")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "Main-Class") {
		t.Errorf("error %q should mention Main-Class", err)
	}
}

func TestValidateJARMainClass_NoManifest(t *testing.T) {
	path := makeJAR(t, "OMIT-MANIFEST")
	err := ValidateJARMainClass(path, "org.tron.program.FullNode")
	if err == nil {
		t.Fatal("expected missing-manifest error")
	}
}

func TestValidateJARMainClass_NoMainClassHeader(t *testing.T) {
	path := makeJAR(t, "")
	err := ValidateJARMainClass(path, "org.tron.program.FullNode")
	if err == nil {
		t.Fatal("expected missing Main-Class error")
	}
}

// TestValidateJARMainClass_ContinuationLines is the FR-011 regression
// guard for JAR manifest's 72-byte line wrap. A long FQN must be
// reassembled across continuation lines before comparison.
func TestValidateJARMainClass_ContinuationLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrapped.jar")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	// Simulate a 72-byte-wrap. Continuation lines start with a single
	// space and are concatenated WITHOUT that space.
	_, _ = w.Write([]byte("Manifest-Version: 1.0\n"))
	_, _ = w.Write([]byte("Main-Class: com.example.really.long.fully.qualified.path.to.the\n"))
	_, _ = w.Write([]byte(" .MainClassName\n"))
	_, _ = w.Write([]byte("Implementation-Title: example\n"))
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := "com.example.really.long.fully.qualified.path.to.the.MainClassName"
	if err := ValidateJARMainClass(path, want); err != nil {
		t.Errorf("continuation-line Main-Class not reassembled: %v", err)
	}
}
