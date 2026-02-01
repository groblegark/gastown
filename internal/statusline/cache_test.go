package statusline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheSaveLoad(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	daemonDir := filepath.Join(tmpDir, "daemon")
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a cache
	cache := &Cache{
		UpdatedAt:  time.Now(),
		Identities: make(map[string]*IdentityData),
		Rigs:       make(map[string]*RigStatus),
	}

	// Add test data
	cache.SetIdentity("test/agent", &IdentityData{
		HookedWork:  "gt-123: Test work",
		MailUnread:  2,
		MailSubject: "Test subject",
	})
	cache.Rigs["testrip"] = &RigStatus{
		HasWitness:  true,
		HasRefinery: true,
		OpState:     "OPERATIONAL",
	}

	// Save
	if err := cache.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	cachePath := CachePath(tmpDir)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Fatal("Cache file not created")
	}

	// Load
	loaded := LoadCache(tmpDir)
	if loaded == nil {
		t.Fatal("LoadCache returned nil")
	}

	// Verify data
	if loaded.GetIdentity("test/agent") == nil {
		t.Fatal("Identity not loaded")
	}

	id := loaded.GetIdentity("test/agent")
	if id.HookedWork != "gt-123: Test work" {
		t.Errorf("HookedWork = %q, want %q", id.HookedWork, "gt-123: Test work")
	}
	if id.MailUnread != 2 {
		t.Errorf("MailUnread = %d, want %d", id.MailUnread, 2)
	}

	if loaded.Rigs["testrip"] == nil {
		t.Fatal("Rig status not loaded")
	}
	if !loaded.Rigs["testrip"].HasWitness {
		t.Error("HasWitness = false, want true")
	}
}

func TestCacheIsStale(t *testing.T) {
	tests := []struct {
		name      string
		updatedAt time.Time
		wantStale bool
	}{
		{
			name:      "fresh cache",
			updatedAt: time.Now(),
			wantStale: false,
		},
		{
			name:      "slightly old cache",
			updatedAt: time.Now().Add(-1 * time.Minute),
			wantStale: false,
		},
		{
			name:      "stale cache",
			updatedAt: time.Now().Add(-3 * time.Minute),
			wantStale: true,
		},
		{
			name:      "very stale cache",
			updatedAt: time.Now().Add(-1 * time.Hour),
			wantStale: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &Cache{UpdatedAt: tt.updatedAt}
			if got := cache.IsStale(); got != tt.wantStale {
				t.Errorf("IsStale() = %v, want %v", got, tt.wantStale)
			}
		})
	}
}

func TestNilCacheIsStale(t *testing.T) {
	var cache *Cache
	if !cache.IsStale() {
		t.Error("nil cache should be stale")
	}
}

func TestCacheGetIdentity(t *testing.T) {
	cache := &Cache{
		Identities: map[string]*IdentityData{
			"test/agent": {HookedWork: "test"},
		},
	}

	// Existing identity
	if id := cache.GetIdentity("test/agent"); id == nil {
		t.Error("GetIdentity returned nil for existing identity")
	}

	// Non-existing identity
	if id := cache.GetIdentity("nonexistent"); id != nil {
		t.Error("GetIdentity returned non-nil for non-existing identity")
	}

	// Nil cache
	var nilCache *Cache
	if id := nilCache.GetIdentity("test/agent"); id != nil {
		t.Error("GetIdentity on nil cache returned non-nil")
	}
}

func TestCacheSetIdentity(t *testing.T) {
	cache := &Cache{}

	// Set on nil map should create it
	cache.SetIdentity("test/agent", &IdentityData{HookedWork: "test"})

	if cache.Identities == nil {
		t.Error("SetIdentity did not create Identities map")
	}
	if cache.GetIdentity("test/agent") == nil {
		t.Error("SetIdentity did not store the identity")
	}
}

func TestLoadCacheNonExistent(t *testing.T) {
	cache := LoadCache("/nonexistent/path")
	if cache != nil {
		t.Error("LoadCache should return nil for non-existent path")
	}
}
