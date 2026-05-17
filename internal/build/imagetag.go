package build

import (
	"fmt"
	"regexp"
)

// imageTagPattern is a pragmatic subset of Docker's reference format.
// Accepts `foo:bar`, `myorg/foo:bar`, `localhost/foo:bar`, optionally
// with a digest. Rejects whitespace, uppercase repo names, path
// traversal, and other inputs that would cause docker CLI confusion.
//
// Spec: docker.io's image reference grammar. This is the simplified
// regex used by the docker CLI for `docker tag` validation.
var imageTagPattern = regexp.MustCompile(
	`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`,
)

// ValidateImageTag enforces FR-005's image_tag check. Surface as
// VALIDATION_ERROR via the caller.
func ValidateImageTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("image_tag is required when artifact = image")
	}
	if !imageTagPattern.MatchString(tag) {
		return fmt.Errorf("invalid image_tag %q: must match Docker reference format <repo>:<tag>", tag)
	}
	return nil
}
