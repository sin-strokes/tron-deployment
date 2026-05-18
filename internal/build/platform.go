package build

import "runtime"

// defaultPlatformForHost mirrors intent.DefaultPlatform's rule
// without importing the intent package (avoids an import cycle —
// intent already imports nothing from build, and we keep it that way).
// Both surfaces produce identical strings; the duplication is small
// enough to live with, but each is unit-tested separately so drift
// would be caught.
func defaultPlatformForHost() string {
	switch runtime.GOARCH {
	case "arm64":
		return "linux/arm64"
	default:
		return "linux/amd64"
	}
}

// defaultJDKForPlatform mirrors intent.DefaultJDKForPlatform.
// Per java-tron's published compat matrix:
//
//	linux/amd64 → JDK 8  (legacy, only tested combo on Intel)
//	linux/arm64 → JDK 17 (only mature arm64 JIT)
func defaultJDKForPlatform(platform string) string {
	switch platform {
	case "linux/arm64":
		return "17"
	default:
		return "8"
	}
}
