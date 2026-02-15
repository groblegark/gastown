// Package reconciler — image digest tracking for automatic pod cycling.
//
// When agent pods use :latest or other mutable tags, the reconciler can't
// detect image changes by comparing tag strings alone. This file adds digest-
// aware drift detection: it compares the running pod's actual image digest
// (from pod.Status.ContainerStatuses[].ImageID) against the newest known
// digest for that image tag. When a newer digest is available, the pod is
// flagged for recreation.
package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ImageDigestTracker caches the latest known digest for each image tag
// and periodically checks the OCI registry for updates.
type ImageDigestTracker struct {
	mu       sync.RWMutex
	digests  map[string]string // image ref (repo:tag) → latest known digest
	logger   *slog.Logger
	interval time.Duration
	client   *http.Client
}

// NewImageDigestTracker creates a tracker that checks the registry at the
// given interval. Pass 0 to disable periodic registry checks (digest will
// still be learned from running pods).
func NewImageDigestTracker(logger *slog.Logger, checkInterval time.Duration) *ImageDigestTracker {
	return &ImageDigestTracker{
		digests:  make(map[string]string),
		logger:   logger,
		interval: checkInterval,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// LatestDigest returns the latest known digest for an image, or "" if unknown.
func (t *ImageDigestTracker) LatestDigest(image string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.digests[image]
}

// RecordDigest records an observed digest for an image. If the digest is
// newer (different), it updates the cache and returns true.
func (t *ImageDigestTracker) RecordDigest(image, digest string) bool {
	if digest == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old := t.digests[image]
	if old == digest {
		return false
	}
	t.digests[image] = digest
	if old != "" {
		t.logger.Info("image digest changed",
			"image", image,
			"old_digest", truncDigest(old),
			"new_digest", truncDigest(digest))
	}
	return old != ""
}

// CheckRegistryDigest queries the OCI registry for the current digest of
// an image tag. Uses the Docker Registry v2 manifest API.
// Returns the digest string (e.g. "sha256:abc123...") or error.
func (t *ImageDigestTracker) CheckRegistryDigest(ctx context.Context, image string) (string, error) {
	repo, tag := parseImageRef(image)
	if repo == "" {
		return "", fmt.Errorf("invalid image reference: %s", image)
	}

	// Determine registry and API URL.
	registry, path := splitRegistryPath(repo)
	if registry == "" || path == "" {
		return "", fmt.Errorf("cannot parse registry from image: %s", image)
	}

	// Get auth token for GHCR (anonymous pull).
	token, err := t.getGHCRToken(ctx, path)
	if err != nil {
		return "", fmt.Errorf("getting auth token: %w", err)
	}

	// Query manifest to get digest.
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, path, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest query returned %d", resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("no Docker-Content-Digest header in response")
	}

	return digest, nil
}

// RefreshImages checks the registry for digest updates on all tracked images.
func (t *ImageDigestTracker) RefreshImages(ctx context.Context) {
	t.mu.RLock()
	images := make([]string, 0, len(t.digests))
	for img := range t.digests {
		images = append(images, img)
	}
	t.mu.RUnlock()

	for _, img := range images {
		digest, err := t.CheckRegistryDigest(ctx, img)
		if err != nil {
			t.logger.Debug("failed to check registry digest",
				"image", img, "error", err)
			continue
		}
		t.RecordDigest(img, digest)
	}
}

// getGHCRToken gets an anonymous pull token from GHCR.
func (t *ImageDigestTracker) getGHCRToken(ctx context.Context, repo string) (string, error) {
	tokenURL := fmt.Sprintf("https://ghcr.io/token?scope=repository:%s:pull", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}

	return tokenResp.Token, nil
}

// parseImageRef splits "ghcr.io/org/repo:tag" into ("ghcr.io/org/repo", "tag").
func parseImageRef(image string) (string, string) {
	// Handle digest references (repo@sha256:...)
	if strings.Contains(image, "@") {
		return "", "" // digest refs don't need tracking
	}

	parts := strings.SplitN(image, ":", 2)
	repo := parts[0]
	tag := "latest"
	if len(parts) == 2 {
		tag = parts[1]
	}
	return repo, tag
}

// splitRegistryPath splits "ghcr.io/org/repo" into ("ghcr.io", "org/repo").
func splitRegistryPath(repo string) (string, string) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// truncDigest returns the first 12 chars of a digest for logging.
func truncDigest(digest string) string {
	// Remove "sha256:" prefix if present, then truncate
	d := strings.TrimPrefix(digest, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// extractDigestFromImageID extracts the digest from a pod's imageID field.
// imageID format: "ghcr.io/org/repo@sha256:abc123..."
func extractDigestFromImageID(imageID string) string {
	if idx := strings.Index(imageID, "@"); idx >= 0 {
		return imageID[idx+1:]
	}
	return ""
}
