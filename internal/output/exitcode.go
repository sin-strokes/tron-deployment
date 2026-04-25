package output

// Exit codes per contracts/cli-contract.md.
// Stable across minor versions.
const (
	ExitSuccess           = 0  // Operation completed successfully or no changes needed
	ExitGeneralError      = 1  // Unclassified error
	ExitValidationError   = 2  // Intent file or config validation failed
	ExitTargetUnreachable = 3  // SSH connection failed or Docker not available
	ExitPreflightFailure  = 4  // Target does not meet requirements
	ExitPartialSuccess    = 5  // Multi-node operation: some succeeded, some failed
	ExitHumanRequired     = 10 // Destructive change in non-interactive mode without --auto-approve
)

// ExitCodeName returns a human-readable name for an exit code.
func ExitCodeName(code int) string {
	switch code {
	case ExitSuccess:
		return "SUCCESS"
	case ExitGeneralError:
		return "GENERAL_ERROR"
	case ExitValidationError:
		return "VALIDATION_ERROR"
	case ExitTargetUnreachable:
		return "TARGET_UNREACHABLE"
	case ExitPreflightFailure:
		return "PREFLIGHT_FAILURE"
	case ExitPartialSuccess:
		return "PARTIAL_SUCCESS"
	case ExitHumanRequired:
		return "HUMAN_REQUIRED"
	default:
		return "UNKNOWN"
	}
}
