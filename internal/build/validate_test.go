package build

import (
	"strings"
	"testing"
)

func TestValidateGradleTask(t *testing.T) {
	tests := []struct {
		name    string
		want    bool // want ok
		comment string
	}{
		{"shadowJar", true, "common case"},
		{":dbfork:build", true, "nested task path"},
		{"assemble", true, "single token"},
		{"my-task", true, "hyphen permitted"},
		{"my_task", true, "underscore permitted"},
		{"", false, "empty rejected"},
		{"123task", false, "must start with letter"},
		{"shadow Jar", false, "no whitespace"},
		{"shadow;rm", false, "no semicolon"},
		{"shadow$()", false, "no $()"},
		{"shadow`evil`", false, "no backticks"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGradleTask(tc.name)
			ok := err == nil
			if ok != tc.want {
				t.Errorf("ValidateGradleTask(%q) ok=%v; want %v (%s) err=%v",
					tc.name, ok, tc.want, tc.comment, err)
			}
		})
	}
}

func TestValidateGradleArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    bool
		comment string
	}{
		{"offline", []string{"--offline"}, true, "common bareword"},
		{"no-daemon", []string{"--no-daemon"}, true, "bareword"},
		{"max-workers", []string{"--max-workers=4"}, true, "kv with int"},
		{"max-workers-bad", []string{"--max-workers=abc"}, false, "non-int rejected"},
		{"max-workers-empty", []string{"--max-workers="}, false, "empty value"},
		{"D-prop", []string{"-Dversion=1.2.3"}, true, "system property"},
		{"P-prop", []string{"-Pcustom=value"}, true, "project property"},
		{"D-prop-space", []string{"-Dtitle=my title"}, true, "value with space (argv-safe)"},
		{"D-prop-comma", []string{"-Dprojects=a,b,c"}, true, "value with comma (argv-safe)"},
		{"D-malformed", []string{"-Dnokey"}, false, "missing equals"},
		{"D-empty", []string{"-D"}, false, "lone -D"},
		{"q", []string{"-q"}, true, "log level"},
		{"init-script-blocked", []string{"--init-script", "/tmp/evil.gradle"}, false, "must be rejected (FR-022 dangerous flag)"},
		{"include-build-blocked", []string{"--include-build", "/tmp/x"}, false, "redirects build"},
		{"build-file-blocked", []string{"--build-file", "/tmp/x"}, false, "redirects build"},
		{"settings-file-blocked", []string{"--settings-file", "/tmp/x"}, false, "redirects build"},
		{"empty-arg", []string{""}, false, "empty rejected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGradleArgs(tc.args)
			ok := err == nil
			if ok != tc.want {
				t.Errorf("ValidateGradleArgs(%v) ok=%v; want %v (%s) err=%v",
					tc.args, ok, tc.want, tc.comment, err)
			}
		})
	}
}

func TestValidateEnvKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"GRADLE_OPTS", "GRADLE_OPTS", true},
		{"JAVA_OPTS", "JAVA_OPTS", true},
		{"GRADLE_USER_HOME", "GRADLE_USER_HOME", true},
		{"MAVEN_OPTS", "MAVEN_OPTS", true},
		{"ORG_GRADLE_PROJECT_foo", "ORG_GRADLE_PROJECT_foo", true},
		{"ORG_GRADLE_PROJECT_BAR", "ORG_GRADLE_PROJECT_BAR", true},
		{"PATH-blocked", "PATH", false},
		{"LD_PRELOAD-blocked", "LD_PRELOAD", false},
		{"DYLD_INSERT_LIBRARIES-blocked", "DYLD_INSERT_LIBRARIES", false},
		{"JAVA_TOOL_OPTIONS-blocked", "JAVA_TOOL_OPTIONS", false},
		{"empty-rejected", "", false},
		{"prefix-only", "ORG_GRADLE_PROJECT_", false}, // suffix required
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEnvKey(tc.key)
			ok := err == nil
			if ok != tc.want {
				t.Errorf("ValidateEnvKey(%q) ok=%v; want %v; err=%v",
					tc.key, ok, tc.want, err)
			}
		})
	}
}

func TestValidateImageTag(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"trond-build:dev", true},
		{"myorg/trond-build:1.2.3", true},
		{"localhost/foo:bar", true},
		{"my.registry.example/path/to/img:1.0", true},
		{"", false},
		{"/etc/passwd", false},
		{"UPPER:case", false},
		{"foo bar:baz", false},
		{"foo:bar baz", false},
		{"foo", false},   // missing tag
		{"foo:", false},  // empty tag
		{":bar", false},  // empty repo
	}
	for _, tc := range tests {
		t.Run(tc.tag, func(t *testing.T) {
			err := ValidateImageTag(tc.tag)
			ok := err == nil
			if ok != tc.want {
				t.Errorf("ValidateImageTag(%q) ok=%v; want %v; err=%v",
					tc.tag, ok, tc.want, err)
			}
		})
	}
}

// TestValidateGradleArgs_RejectMessage asserts the error mentions
// the spec FR so an operator hitting it can search the codebase.
func TestValidateGradleArgs_RejectMessage(t *testing.T) {
	err := ValidateGradleArgs([]string{"--init-script", "/tmp/x"})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "FR-022") {
		t.Errorf("error %q should reference FR-022", err)
	}
}
