package build

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ValidateGradleTask enforces FR-022's task-name regex. Task names
// are inherently regular ("shadowJar", ":dbfork:build", "assemble").
// Tight regex is fine here. Allows an optional leading `:` (gradle
// absolute task path) and standard project/task name characters.
var gradleTaskPattern = regexp.MustCompile(`^:?[a-zA-Z][a-zA-Z0-9_-]*(:[a-zA-Z][a-zA-Z0-9_-]*)*$`)

// ValidateGradleTask returns nil if name is a syntactically safe
// gradle task identifier.
func ValidateGradleTask(name string) error {
	if name == "" {
		return fmt.Errorf("gradle_task is required")
	}
	if !gradleTaskPattern.MatchString(name) {
		return fmt.Errorf("invalid gradle_task %q: must match %s",
			name, gradleTaskPattern.String())
	}
	return nil
}

// ValidateGradleArgs enforces FR-022's gradle_args flag-name
// allowlist. Per the spec rationale: character regexes are the wrong
// defense (--init-script /tmp/evil.gradle passes any sane char regex
// while --projects=a,b,c fails one), so we whitelist flag names
// instead. argv-form invocation already blocks shell injection;
// what's left is "which gradle flags are dangerous".
//
// Accepted shapes:
//
//	--offline, --no-daemon, --parallel, --rerun-tasks
//	--max-workers=<int>
//	-D<key>=<val>, -P<key>=<val>   (value unrestricted; argv-safe)
//	-q, -i, -d
//
// Anything else is rejected. The dangerous flags we specifically
// want to forbid: --init-script, --include-build, --build-file,
// --settings-file (they redirect the build to attacker-supplied
// logic).
func ValidateGradleArgs(args []string) error {
	for _, a := range args {
		if err := validateGradleArg(a); err != nil {
			return err
		}
	}
	return nil
}

func validateGradleArg(a string) error {
	if a == "" {
		return fmt.Errorf("empty gradle_arg")
	}
	switch {
	case a == "--offline",
		a == "--no-daemon",
		a == "--parallel",
		a == "--rerun-tasks",
		a == "-q", a == "-i", a == "-d":
		return nil
	}
	// --max-workers=<int>
	if strings.HasPrefix(a, "--max-workers=") {
		v := strings.TrimPrefix(a, "--max-workers=")
		if v == "" {
			return fmt.Errorf("--max-workers requires an integer value")
		}
		for _, r := range v {
			if r < '0' || r > '9' {
				return fmt.Errorf("--max-workers value %q must be a positive integer", v)
			}
		}
		return nil
	}
	// -D<key>=<val> / -P<key>=<val>
	if (strings.HasPrefix(a, "-D") || strings.HasPrefix(a, "-P")) && len(a) > 2 {
		eq := strings.IndexByte(a[2:], '=')
		if eq <= 0 {
			return fmt.Errorf("malformed %s flag %q: expected -%c<key>=<value>",
				a[:2], a, a[1])
		}
		// Value is intentionally unrestricted — argv-form makes shell
		// interpretation impossible; gradle treats the value as a
		// plain string.
		return nil
	}
	return fmt.Errorf("disallowed gradle_arg %q: only --offline, --no-daemon, "+
		"--parallel, --rerun-tasks, --max-workers=N, -D<k>=<v>, -P<k>=<v>, "+
		"-q/-i/-d are permitted (see spec FR-022)", a)
}

// envAllowlist enforces FR-019. Allowlisted env keys are forwarded
// from the trond invocation environment AND accepted in
// `build.env: { KEY: VAL }`. Everything else is rejected to prevent
// LD_PRELOAD / PATH hijacks of the build container.
var envAllowlist = map[string]struct{}{
	"GRADLE_OPTS":      {},
	"JAVA_OPTS":        {},
	"GRADLE_USER_HOME": {},
	"MAVEN_OPTS":       {},
}

const orgGradleProjectPrefix = "ORG_GRADLE_PROJECT_"

// ValidateEnvKey returns nil if the env var name is on the allowlist
// (literal or prefix).
func ValidateEnvKey(name string) error {
	if name == "" {
		return fmt.Errorf("empty env key")
	}
	if _, ok := envAllowlist[name]; ok {
		return nil
	}
	if strings.HasPrefix(name, orgGradleProjectPrefix) && len(name) > len(orgGradleProjectPrefix) {
		return nil
	}
	return fmt.Errorf("disallowed env key %q: only %s and ORG_GRADLE_PROJECT_* "+
		"are forwarded into the build container (spec FR-019)",
		name, allowedKeysString())
}

func allowedKeysString() string {
	keys := make([]string, 0, len(envAllowlist))
	for k := range envAllowlist {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

// ValidateJARMainClass opens a built jar and confirms its
// META-INF/MANIFEST.MF Main-Class header equals expected (e.g.
// "org.tron.program.FullNode"). Returns a structured error otherwise.
//
// FR-011: produced JAR must be runnable as a java-tron node.
func ValidateJARMainClass(path, expected string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open jar: %w", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("read manifest: %w", err)
		}
		defer rc.Close()
		got, err := scanManifestMainClass(rc)
		if err != nil {
			return err
		}
		if got != expected {
			return fmt.Errorf("jar Main-Class = %q; want %q", got, expected)
		}
		return nil
	}
	return fmt.Errorf("jar has no META-INF/MANIFEST.MF")
}

// scanManifestMainClass extracts the Main-Class value from a JAR
// manifest. Honors the JAR-manifest spec's line-continuation rule:
// values longer than 72 bytes wrap to the next line with a leading
// single space, and the continuation belongs to the previous header.
//
// Without continuation handling, a long FQN like
// `com.example.really.long.package.path.MainClass` would be split
// across two lines and the validator would compare against only the
// first half. java-tron's current `org.tron.program.FullNode` is too
// short to trigger this, but the manifest spec is the manifest spec.
func scanManifestMainClass(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	const prefix = "Main-Class:"
	var (
		building string // value being accumulated (only set when we're inside Main-Class)
		found    bool
	)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, " ") && found:
			// Continuation line — append (without the leading space).
			building += strings.TrimPrefix(line, " ")
		case strings.HasPrefix(line, prefix):
			building = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			found = true
		case found:
			// A different header began; Main-Class is complete.
			return building, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan manifest: %w", err)
	}
	if !found {
		return "", fmt.Errorf("manifest has no Main-Class header")
	}
	return building, nil
}
