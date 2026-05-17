package build

import "context"

// TestRunner is the abstract signature callers outside this package
// substitute for the real docker-shelling-out runner. Mirrors the
// internal dockerRunner interface. Lives in a non-_test.go file so
// it's accessible from other packages' tests (which can't import
// _test files), but kept on a narrow surface.
type TestRunner interface {
	RunDockerBuild(ctx context.Context, sourcePath, outTmpPath string, gradleTask string, gradleArgs []string, env map[string]string) error
}

// SetTestRunner swaps the package-level runner with a caller-supplied
// stub for the duration of the returned restore func. Intended for
// tests in other packages (internal/apply, cmd/, MCP) that need to
// drive Run() without an actual docker daemon. Returns a restore
// func that the caller defers.
//
// Example:
//
//	restore := build.SetTestRunner(myStub)
//	defer restore()
//	build.Run(ctx, req)
func SetTestRunner(stub TestRunner) (restore func()) {
	orig := defaultRunner
	defaultRunner = &adapter{stub: stub}
	return func() { defaultRunner = orig }
}

type adapter struct {
	stub TestRunner
}

func (a *adapter) RunDockerBuild(ctx context.Context, r *resolved, outDir, outTmp string) error {
	return a.stub.RunDockerBuild(ctx, r.req.SourcePath, outTmp,
		r.req.GradleTask, r.req.GradleArgs, r.req.Env)
}
