package podmanager

import (
	"fmt"
	"strings"
)

// ValidateImageRegistry checks if an image reference matches the allowlist.
// Returns nil if the image is allowed, or an error describing the rejection.
// An empty allowlist allows all images.
func ValidateImageRegistry(image string, allowlist []string) error {
	if len(allowlist) == 0 {
		return nil // no allowlist = allow all
	}
	for _, prefix := range allowlist {
		if strings.HasPrefix(image, prefix) {
			return nil
		}
	}
	return fmt.Errorf("image %q not in registry allowlist %v", image, allowlist)
}

// hasLatestOrNoTag returns true if the image ref has no tag, has :latest,
// or uses a digest (sha256:...) — all cases where we want PullAlways
// except digests which are immutable and use IfNotPresent.
func hasLatestOrNoTag(image string) bool {
	// Strip registry prefix to find tag portion.
	// Image refs: "registry/repo:tag", "registry/repo@sha256:...", "registry/repo"
	if strings.Contains(image, "@") {
		// Digest reference — immutable, no need to always pull.
		return false
	}
	// Find tag after last colon that's not part of a port (port is before /).
	lastSlash := strings.LastIndex(image, "/")
	tagPart := image
	if lastSlash >= 0 {
		tagPart = image[lastSlash:]
	}
	colonIdx := strings.LastIndex(tagPart, ":")
	if colonIdx < 0 {
		// No tag at all — treated as :latest by container runtime.
		return true
	}
	tag := tagPart[colonIdx+1:]
	return tag == "latest"
}
