package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var versionCheckUpdate bool

// versionCmd is the structured / scriptable counterpart to the cobra-built-in
// --version flag. It always prints the local build identifiers, and with
// --check-update queries GitHub releases for the latest tag and compares.
//
// We hit api.github.com directly with a 5-second timeout. Failure to reach
// GitHub is reported as a warning but does NOT make the command fail —
// "I couldn't check for updates" is information, not an error.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information; optionally compare against latest release",
	RunE:  runVersion,
}

func init() {
	versionCmd.Flags().BoolVar(&versionCheckUpdate, "check-update", false, "Query GitHub for the latest release and compare")
	rootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	info := map[string]any{
		"version":    version,
		"commit":     commit,
		"build_time": buildTime,
	}

	if versionCheckUpdate {
		latest, checkErr := fetchLatestRelease(cmd.Context())
		switch {
		case checkErr != nil:
			info["update_check"] = "failed: " + checkErr.Error()
		case latest == "":
			info["update_check"] = "no releases found"
		default:
			info["latest"] = latest
			info["update_available"] = isNewer(latest, version)
		}
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, info)
	}

	fmt.Printf("trond version %s (commit %s, built %s)\n", info["version"], info["commit"], info["build_time"])
	if versionCheckUpdate {
		switch v := info["update_check"].(type) {
		case string:
			fmt.Fprintf(os.Stderr, "Update check: %s\n", v)
		default:
			latest := info["latest"].(string)
			if info["update_available"].(bool) {
				fmt.Printf("Update available: %s → %s\n", info["version"], latest)
				fmt.Println("Download: https://github.com/tronprotocol/tron-deployment/releases/latest")
			} else {
				fmt.Printf("Up to date (latest %s)\n", latest)
			}
		}
	}
	return nil
}

// fetchLatestRelease returns the most recent release tag from GitHub.
// Empty tag with nil error means no releases yet.
func fetchLatestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/tronprotocol/tron-deployment/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "trond/"+version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No releases published yet — not an error.
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// isNewer reports whether `latest` is strictly newer than `current`.
// Both are treated as semver-ish tags (`v0.1.2`, optional `-alpha.3`).
// We compare lexicographically after dropping a leading "v" — good enough
// for the trond release cadence; users who expect strict semver
// pre-release ordering can ship their own check.
func isNewer(latest, current string) bool {
	l := strings.TrimPrefix(latest, "v")
	c := strings.TrimPrefix(current, "v")
	// Local dev builds carry "-dirty" or commit short SHAs that aren't
	// meaningfully comparable to a release tag — flag those as "newer
	// available" only when the latest tag string differs.
	if strings.Contains(c, "-dirty") || !strings.HasPrefix(current, "v") {
		return l != c
	}
	return l > c
}
