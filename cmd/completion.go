package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// We override the default `completion` subcommand cobra ships with so we
// can add a `--install` convenience that drops the script in the right
// place for the user's shell. The default behaviour (`trond completion bash`
// printing to stdout) is preserved by NOT removing the original — cobra
// still generates the per-shell scripts; we wrap them.

var completionInstall bool

func init() {
	// cobra has already registered a default `completion` command before
	// our init runs. We attach a flag to it and tweak its RunE to honor
	// --install when set, deferring to the original generator otherwise.
	for _, c := range rootCmd.Commands() {
		if c.Name() != "completion" {
			continue
		}
		c.Flags().BoolVar(&completionInstall, "install", false, "Write the script to the shell's standard location instead of stdout")
		// Wrap the original RunE/PersistentRunE.
		oldRun := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if !completionInstall {
				if oldRun != nil {
					return oldRun(cmd, args)
				}
				return nil
			}
			if len(args) != 1 {
				return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
					"--install needs exactly one shell argument: bash | zsh | fish | powershell")
			}
			return installCompletion(args[0])
		}
		break
	}
}

// installCompletion writes the generated completion script to the
// well-known location for the user's shell. We only target the user
// scope (no /etc/* writes) so installation never needs sudo.
func installCompletion(shell string) error {
	dest, err := completionDest(shell)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return output.NewError("INSTALL_ERROR", output.ExitGeneralError, err.Error())
	}
	f, err := os.Create(dest)
	if err != nil {
		return output.NewError("INSTALL_ERROR", output.ExitGeneralError, err.Error())
	}
	defer f.Close()

	switch shell {
	case "bash":
		err = rootCmd.GenBashCompletionV2(f, true)
	case "zsh":
		err = rootCmd.GenZshCompletion(f)
	case "fish":
		err = rootCmd.GenFishCompletion(f, true)
	case "powershell":
		err = rootCmd.GenPowerShellCompletionWithDesc(f)
	default:
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"unsupported shell: "+shell)
	}
	if err != nil {
		return output.NewError("INSTALL_ERROR", output.ExitGeneralError, err.Error())
	}

	fmt.Fprintf(os.Stderr, "trond completion installed for %s at %s\n", shell, dest)
	fmt.Fprintln(os.Stderr, completionFollowupHint(shell, dest))
	return nil
}

// completionDest returns the per-user path each shell auto-loads from.
// We don't try to detect distro-specific overrides; users who customise
// FPATH/COMPLETION_PATH can pipe the output to wherever they like via
// the default (no --install) form.
func completionDest(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shell {
	case "bash":
		// bash-completion v2 looks at ~/.local/share/bash-completion/completions/<cmd>.
		return filepath.Join(home, ".local", "share", "bash-completion", "completions", "trond"), nil
	case "zsh":
		// zsh: ~/.config/zsh/completions/_trond, requires fpath entry.
		return filepath.Join(home, ".config", "zsh", "completions", "_trond"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "completions", "trond.fish"), nil
	case "powershell":
		// PowerShell: profile.d-style — printed to stderr for the user
		// to source from their $PROFILE.
		return filepath.Join(home, ".config", "powershell", "trond.ps1"), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

func completionFollowupHint(shell, dest string) string {
	switch shell {
	case "bash":
		return "Restart your shell, or: source " + dest
	case "zsh":
		return "Add this once to ~/.zshrc:\n  fpath=(" + filepath.Dir(dest) + " $fpath)\n  autoload -U compinit && compinit\n  Then restart your shell."
	case "fish":
		return "Restart fish to pick up the new completion."
	case "powershell":
		// For PowerShell:
		path := dest
		if runtime.GOOS == "windows" {
			path = filepath.ToSlash(dest)
		}
		return ". " + path + "  # add this line to your $PROFILE"
	}
	return ""
}
