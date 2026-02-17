package podmanager

import (
	"testing"
)

func TestHasLatestOrNoTag(t *testing.T) {
	tests := []struct {
		image string
		want  bool
	}{
		{"nginx", true},
		{"nginx:latest", true},
		{"nginx:1.21", false},
		{"ghcr.io/org/img:latest", true},
		{"ghcr.io/org/img:v1.0", false},
		{"ghcr.io/org/img", true},
		{"ghcr.io/org/img@sha256:abc123", false},
		{"registry.local:5000/img", true},
		{"registry.local:5000/img:latest", true},
		{"registry.local:5000/img:v1", false},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := hasLatestOrNoTag(tt.image)
			if got != tt.want {
				t.Errorf("hasLatestOrNoTag(%q) = %v, want %v", tt.image, got, tt.want)
			}
		})
	}
}

func TestValidateImageRegistry(t *testing.T) {
	allowlist := []string{"ghcr.io/groblegark/", "docker.io/library/"}

	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{"allowed ghcr image", "ghcr.io/groblegark/toolchain:v1", false},
		{"allowed docker library", "docker.io/library/node:18", false},
		{"rejected unknown registry", "evil.io/malware:latest", true},
		{"rejected similar prefix", "ghcr.io/groblegarkx/bad:v1", true},
		{"rejected empty image", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageRegistry(tt.image, allowlist)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImageRegistry(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
		})
	}
}

func TestValidateImageRegistry_EmptyAllowlist(t *testing.T) {
	err := ValidateImageRegistry("evil.io/anything:latest", nil)
	if err != nil {
		t.Error("empty allowlist should allow all images")
	}
	err = ValidateImageRegistry("evil.io/anything:latest", []string{})
	if err != nil {
		t.Error("empty slice allowlist should allow all images")
	}
}
