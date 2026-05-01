package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var (
	dlNetwork  string
	dlDomain   string
	dlKind     string
	dlRegion   string
	dlEngine   string
	dlBackup   string
	dlDest     string
	dlNode     string
	dlForce    bool
	dlNoVerify bool
	dlDryRun   bool
	dlDetach   bool
)

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Stream a snapshot tarball into a local directory",
	Long: `Download a chain database snapshot, streaming the tarball through
gunzip + tar so the .tgz is never persisted to disk. Verifies the
upstream MD5 sidecar (when published), pre-checks free disk space, and
refuses to overwrite an existing database without --force.

The default destination is ./output-directory under the current working
directory — same convention as the official tron-docker tooling. Pass
--node <name> to write into a managed node's volume / install path
instead.`,
	Example: `  # Latest mainnet lite snapshot, default mirror
  trond snapshot download --network mainnet

  # Pin to a specific backup, full archive, US mirror
  trond snapshot download --network mainnet --type full --region america --backup backup20250115

  # Show what would happen without downloading
  trond snapshot download --network nile --dry-run

  # Pipe straight into a managed node's storage path
  trond snapshot download --node my-fullnode --network mainnet`,
	RunE: runDownload,
}

func init() {
	downloadCmd.Flags().StringVar(&dlNetwork, "network", "", "Network: mainnet | nile")
	downloadCmd.Flags().StringVar(&dlDomain, "domain", "", "Mirror domain (overrides --network/--region)")
	downloadCmd.Flags().StringVar(&dlKind, "type", "lite", "Snapshot kind: lite | full")
	downloadCmd.Flags().StringVar(&dlRegion, "region", "", "Region: singapore | america")
	downloadCmd.Flags().StringVar(&dlEngine, "db-engine", "", "Engine: leveldb | rocksdb (mainnet full only)")
	downloadCmd.Flags().StringVar(&dlBackup, "backup", "", "Specific backup name (default: latest)")
	downloadCmd.Flags().StringVar(&dlDest, "to", "", "Destination directory (default ./output-directory)")
	downloadCmd.Flags().StringVar(&dlNode, "node", "", "Managed node name; resolves --to from state")
	downloadCmd.Flags().BoolVar(&dlForce, "force", false, "Overwrite existing database in destination")
	downloadCmd.Flags().BoolVar(&dlNoVerify, "no-verify", false, "Skip MD5 verification (not recommended)")
	downloadCmd.Flags().BoolVar(&dlDryRun, "dry-run", false, "Print what would be downloaded and exit")
	downloadCmd.Flags().BoolVar(&dlDetach, "detach", false, "Run in background; survives terminal close (logs to ~/.trond/snapshots/<id>.log)")
}

func runDownload(cmd *cobra.Command, _ []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	src, err := resolveSource(dlDomain, dlNetwork, dlKind, dlRegion, dlEngine)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	backup := dlBackup
	if backup == "" {
		latest, err := snapshot.LatestBackup(cmd.Context(), *src)
		if err != nil {
			return output.NewError("LIST_ERROR", output.ExitGeneralError, err.Error())
		}
		backup = latest
	}

	dest := dlDest
	if dlNode != "" {
		resolved, err := destFromNode(dlNode)
		if err != nil {
			return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
		}
		dest = resolved
	}
	if dest == "" {
		dest = "./output-directory"
	}

	opts := snapshot.DownloadOptions{
		Source:   *src,
		Backup:   backup,
		Kind:     snapshot.DBKind(dlKind),
		DestDir:  dest,
		Force:    dlForce,
		NoVerify: dlNoVerify,
	}

	pre, err := snapshot.Preflight(cmd.Context(), opts)
	if err != nil {
		return output.NewError("PREFLIGHT_ERROR", output.ExitGeneralError, err.Error())
	}

	if dlDryRun {
		return emitPlan(outputFmt, src, backup, dest, pre)
	}

	// Refuse overwrite up front so the user sees the message before
	// the download starts (the same check inside Download fires after
	// preflight again, but we want to surface it pre-network).
	if pre.WouldOverwrite && !dlForce {
		return output.NewError("HUMAN_REQUIRED", output.ExitHumanRequired,
			fmt.Sprintf("destination %s already has a database; pass --force to overwrite",
				filepath.Join(dest, "output-directory", "database")))
	}
	if pre.NeededBytes > 0 && pre.FreeBytes < pre.NeededBytes {
		return output.NewError("DISK_SPACE_ERROR", output.ExitGeneralError,
			fmt.Sprintf("need ~%s free in %s, have %s",
				humanGB(pre.NeededBytes), dest, humanGB(pre.FreeBytes)))
	}

	// Hand off to a detached child if requested. We run the same trond
	// binary with the same args minus --detach, redirected to a log file
	// in the per-user state dir; the child survives terminal close (we
	// disown via Setsid so SIGHUP doesn't reach it). Returns immediately
	// with the job manifest.
	if dlDetach {
		return spawnDetached(outputFmt, src, backup, dest)
	}

	if outputFmt != "json" {
		// Set up a periodic progress printer for the human user. JSON
		// callers see only the final result.
		opts.ProgressFn = makeProgressPrinter(cmd.ErrOrStderr())
	}

	res, err := snapshot.Download(cmd.Context(), opts)
	if err != nil {
		var ow *snapshot.OverwriteError
		if errors.As(err, &ow) {
			return output.NewError("HUMAN_REQUIRED", output.ExitHumanRequired, ow.Error())
		}
		return output.NewError("DOWNLOAD_ERROR", output.ExitGeneralError, err.Error())
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"source":           src,
			"backup":           backup,
			"dest":             dest,
			"bytes_downloaded": res.BytesDownloaded,
			"duration_ms":      res.DurationMs,
			"md5_verified":     res.MD5Verified,
			"actual_md5":       res.ActualMD5,
			"files_extracted":  res.FilesExtracted,
			"userdata_present": pre.UserdataPresent,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Done. %s in %s, %d files extracted to %s",
		humanGB(uint64(res.BytesDownloaded)), res.Duration.Round(time.Second), res.FilesExtracted, dest)
	if res.MD5Verified {
		fmt.Fprint(cmd.OutOrStdout(), " (md5 ✓)")
	} else if !dlNoVerify {
		fmt.Fprint(cmd.OutOrStdout(), " (md5 sidecar absent — not verified)")
	}
	fmt.Fprintln(cmd.OutOrStdout())
	if pre.UserdataPresent {
		fmt.Fprintln(cmd.OutOrStdout(), "Note: pre-existing userdata/ was preserved.")
	}
	return nil
}

func emitPlan(outputFmt string, src *snapshot.Source, backup, dest string, pre *snapshot.PreflightResult) error {
	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"source":    src,
			"backup":    backup,
			"dest":      dest,
			"preflight": pre,
		})
	}
	fmt.Println("Snapshot download plan:")
	fmt.Printf("  source:           %s (%s, %s, %s)\n", src.Domain, src.Network, src.DBKind, src.Region)
	fmt.Printf("  backup:           %s\n", backup)
	fmt.Printf("  url:              %s\n", pre.URL)
	fmt.Printf("  expected size:    %s\n", humanGB(uint64(pre.ExpectedSize)))
	fmt.Printf("  destination:      %s\n", dest)
	fmt.Printf("  free space:       %s\n", humanGB(pre.FreeBytes))
	fmt.Printf("  needed (~2x DL):  %s\n", humanGB(pre.NeededBytes))
	fmt.Printf("  database present: %t\n", pre.DatabasePresent)
	fmt.Printf("  userdata present: %t (preserved across extraction)\n", pre.UserdataPresent)
	fmt.Printf("  md5 sidecar:      %t\n", pre.HasMD5Sidecar)
	if pre.WouldOverwrite {
		fmt.Println("  WARNING: existing database would be overwritten (use --force).")
	}
	if pre.NeededBytes > 0 && pre.FreeBytes < pre.NeededBytes {
		fmt.Println("  WARNING: insufficient disk space for safe extraction.")
	}
	return nil
}

// destFromNode looks up a managed node and returns a path that maps to
// its chain-data root. For docker runtime we point at the named volume
// — but volumes aren't a filesystem path the user can extract into, so
// we surface a helpful error instead. For jar runtime we use install_path.
func destFromNode(name string) (string, error) {
	store, err := state.NewStore(paths.State())
	if err != nil {
		return "", err
	}
	st, err := store.Load()
	if err != nil {
		return "", err
	}
	node := store.GetNode(st, name)
	if node == nil {
		return "", fmt.Errorf("node %q not in state", name)
	}
	if node.Runtime != "jar" {
		return "", fmt.Errorf("--node only supports jar runtime; for docker, extract to a host path "+
			"and bind-mount via storage.path in your intent (current runtime: %s)", node.Runtime)
	}
	if node.InstallPath == "" {
		return "", fmt.Errorf("node %q has no install_path recorded; rerun apply or pass --to", name)
	}
	return node.InstallPath, nil
}

// makeProgressPrinter returns a ProgressFn that emits a single repeating
// status line to stderr — no curses, no bars, just numbers. Plays nicely
// with non-tty environments (CI, nohup) and with the existing log format.
func makeProgressPrinter(w interface{ Write(p []byte) (int, error) }) func(int64, int64) {
	var lastPercent int = -1
	start := time.Now()
	return func(downloaded, total int64) {
		if total <= 0 {
			fmt.Fprintf(w, "\rDownloaded %s, elapsed %s", humanGB(uint64(downloaded)), time.Since(start).Round(time.Second))
			return
		}
		percent := int(float64(downloaded) * 100 / float64(total))
		if percent == lastPercent {
			return
		}
		lastPercent = percent
		eta := "--"
		if downloaded > 0 {
			elapsed := time.Since(start)
			remain := time.Duration(float64(elapsed) * float64(total-downloaded) / float64(downloaded))
			eta = remain.Round(time.Second).String()
		}
		fmt.Fprintf(w, "\r%3d%%  %s / %s  eta %s",
			percent,
			humanGB(uint64(downloaded)),
			humanGB(uint64(total)),
			eta,
		)
		if percent == 100 {
			fmt.Fprintln(w)
		}
	}
}

// humanGB renders a byte count in GB (or "unknown" for zero) — trimmed
// for log lines where seconds-of-progress matter more than precision.
func humanGB(n uint64) string {
	if n == 0 {
		return "unknown"
	}
	const GB = 1 << 30
	if n < GB {
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/float64(GB))
}
