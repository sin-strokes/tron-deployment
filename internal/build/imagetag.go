package build

import (
	"fmt"
	"regexp"
	"strings"
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

// wellKnownTagPrefixes are reserved by upstream projects. Letting
// trond tag a locally-built image as one of these would shadow the
// real upstream image in the host's docker store — `docker images`
// could no longer distinguish trond's artifact from the official
// version, and an operator debugging a deploy would reasonably
// assume the tag refers to the canonical bits.
//
// Keep the list short and focused on the upstream surfaces trond
// genuinely interacts with (java-tron's official image, the builder
// image, and docker hub's default-lib namespace).
var wellKnownTagPrefixes = []string{
	"tronprotocol/",    // upstream java-tron image
	"eclipse-temurin/", // trond's pinned JDK builder image namespace
	"library/",         // docker hub's default lib namespace
}

// ValidateImageTag enforces FR-005's image_tag check. Surface as
// VALIDATION_ERROR via the caller.
func ValidateImageTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("image_tag is required when artifact = image")
	}
	if !imageTagPattern.MatchString(tag) {
		return fmt.Errorf("invalid image_tag %q: must match Docker reference format <repo>:<tag>", tag)
	}
	for _, prefix := range wellKnownTagPrefixes {
		if strings.HasPrefix(tag, prefix) {
			return fmt.Errorf("invalid image_tag %q: prefix %q is reserved for upstream images; pick a trond-specific namespace such as 'trond-build/...' or 'localhost/...'", tag, prefix)
		}
	}
	return nil
}
