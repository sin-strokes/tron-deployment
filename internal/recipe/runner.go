package recipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RunOptions configures a single recipe run.
type RunOptions struct {
	// Binary is the trond executable to invoke for each step. Defaults
	// to os.Args[0]; tests pass a fake binary that emits canned JSON.
	Binary string

	// Params is the user-supplied param map. Keys must match
	// recipe.Param.Name. Required params with no default are validated
	// before any step runs.
	Params map[string]string

	// DryRun prints the resolved command for each step without
	// executing. Useful for "show me what this would do".
	DryRun bool

	// ResumeFrom skips every step before the named ID. The skipped
	// steps' outputs aren't available, so recipes that resume have to
	// be designed to tolerate missing earlier outputs (or the user
	// has to supply them via --param).
	ResumeFrom string

	// Out / Err receive human-readable progress lines. Recipe runs
	// always log structured progress to stderr in addition to the
	// final RunResult.
	Out io.Writer
	Err io.Writer
}

// Run executes a recipe. Returns a RunResult plus error; the error is
// nil on a clean run, non-nil whenever any step's on_failure abort
// fires or when params validation fails.
func Run(ctx context.Context, r Recipe, opts RunOptions) (*RunResult, error) {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Binary == "" {
		opts.Binary = os.Args[0]
	}

	resolved, err := resolveParams(r.Params, opts.Params)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &RunResult{
		Recipe:    r.Name,
		StartedAt: start.UTC().Format(time.RFC3339),
		Status:    "success",
	}
	stepsState := map[string]map[string]any{}
	skipping := opts.ResumeFrom != ""

	for _, step := range r.Steps {
		if skipping {
			if step.ID == opts.ResumeFrom {
				skipping = false
			} else {
				result.Steps = append(result.Steps, StepResult{
					ID:      step.ID,
					Skipped: true,
				})
				continue
			}
		}

		// Skip predicate: render the template, treat literal "true"
		// (case-insensitive) as "skip this step". We evaluate even on
		// dry-run so the planned chain matches the real chain.
		if step.Skip != "" {
			renderedSkip, err := substitute(step.Skip, resolved, stepsState)
			if err != nil {
				return result, fmt.Errorf("step %s: skip template: %w", step.ID, err)
			}
			if strings.EqualFold(strings.TrimSpace(renderedSkip), "true") {
				fmt.Fprintf(opts.Err, "  [%s] skipped (skip-condition true)\n", step.ID)
				result.Steps = append(result.Steps, StepResult{ID: step.ID, Skipped: true})
				continue
			}
		}

		args, err := substituteAll(step.Args, resolved, stepsState)
		if err != nil {
			return result, fmt.Errorf("step %s: %w", step.ID, err)
		}

		stepResult, err := runStep(ctx, opts, step, args)
		result.Steps = append(result.Steps, stepResult)

		if err != nil {
			switch step.OnFailure {
			case "continue":
				fmt.Fprintf(opts.Err, "  [%s] failed (continuing): %v\n", step.ID, err)
				continue
			case "rollback":
				result.Status = "rolled_back"
				result.FailedAt = step.ID
				result.RollbackRan = true
				fmt.Fprintf(opts.Err, "  [%s] failed; running rollback\n", step.ID)
				rolled := runRollback(ctx, opts, r.Rollback, resolved, stepsState)
				result.RollbackSteps = rolled
				result.DurationMs = time.Since(start).Milliseconds()
				return result, fmt.Errorf("step %s failed: %w", step.ID, err)
			default: // "abort" / unset
				result.Status = "failed"
				result.FailedAt = step.ID
				result.DurationMs = time.Since(start).Milliseconds()
				return result, fmt.Errorf("step %s failed: %w", step.ID, err)
			}
		}

		// Persist the named output fields for downstream substitution.
		if len(step.Persist) > 0 && stepResult.Output != nil {
			persisted := map[string]any{}
			for _, k := range step.Persist {
				if v, ok := stepResult.Output[k]; ok {
					persisted[k] = v
				}
			}
			stepsState[step.ID] = persisted
		} else {
			stepsState[step.ID] = stepResult.Output
		}
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// runStep handles a single step's exec + output capture.
func runStep(ctx context.Context, opts RunOptions, step Step, args []string) (StepResult, error) {
	res := StepResult{ID: step.ID}
	if step.Command == "" {
		res.Error = "step has no command"
		return res, errors.New(res.Error)
	}

	full := append(strings.Fields(step.Command), args...)
	full = append(full, "--output", "json")

	if opts.DryRun {
		fmt.Fprintf(opts.Out, "  [%s] would run: %s %s\n", step.ID, opts.Binary, strings.Join(full, " "))
		return res, nil
	}

	fmt.Fprintf(opts.Err, "  [%s] %s %s\n", step.ID, opts.Binary, strings.Join(full, " "))
	start := time.Now()

	cmd := exec.CommandContext(ctx, opts.Binary, full...)
	cmd.Stderr = opts.Err
	stdout, err := cmd.Output()
	res.DurationMs = time.Since(start).Milliseconds()
	res.ExitCode = cmd.ProcessState.ExitCode()
	res.Output = captureOutput(stdout)

	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	return res, nil
}

// runRollback executes every rollback step in order, logging but not
// short-circuiting on failures. Rollback is "best effort cleanup".
func runRollback(ctx context.Context, opts RunOptions, steps []Step, params map[string]string, state map[string]map[string]any) []StepResult {
	out := make([]StepResult, 0, len(steps))
	for _, s := range steps {
		args, err := substituteAll(s.Args, params, state)
		if err != nil {
			out = append(out, StepResult{ID: s.ID, Error: err.Error()})
			continue
		}
		res, err := runStep(ctx, opts, s, args)
		if err != nil {
			fmt.Fprintf(opts.Err, "  rollback [%s] failed (continuing): %v\n", s.ID, err)
		}
		out = append(out, res)
	}
	return out
}

// resolveParams validates user inputs against the recipe's declared
// params and applies defaults. Missing required params are an error
// before any step runs.
func resolveParams(declared []Param, supplied map[string]string) (map[string]string, error) {
	out := map[string]string{}
	declaredByName := map[string]Param{}
	for _, p := range declared {
		declaredByName[p.Name] = p
		if v, ok := supplied[p.Name]; ok {
			out[p.Name] = v
			continue
		}
		if p.Default != "" {
			out[p.Name] = p.Default
			continue
		}
		if p.Required {
			return nil, fmt.Errorf("required param %q not supplied", p.Name)
		}
	}
	// Surface unrecognised params explicitly — silently ignoring them
	// hides typos like --param node-name=... when the recipe declares
	// node_name.
	for k := range supplied {
		if _, ok := declaredByName[k]; !ok {
			return nil, fmt.Errorf("unknown param %q (recipe declares: %s)", k, paramNames(declared))
		}
	}
	return out, nil
}

func paramNames(ps []Param) string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}
