// Package statusline provides caching for tmux status line data.
//
// Problem: With N concurrent Claude sessions, each calling `gt status-line` every
// 5 seconds, we get N queries to Dolt per interval. This causes high CPU load
// on the Dolt server.
//
// Solution: A single daemon process updates a cache file once per interval.
// All status-line calls read from the cache, reducing N queries to 1.
package statusline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CacheFile is the name of the status line cache file within the daemon directory.
const CacheFile = "statusline-cache.json"

// DefaultCacheInterval is how often the cache is refreshed.
const DefaultCacheInterval = 30 * time.Second

// MaxCacheAge is how old the cache can be before it's considered stale.
// Status-lines will fall back to direct queries if cache is older than this.
const MaxCacheAge = 2 * time.Minute

// IdentityData contains cached status data for a single identity.
type IdentityData struct {
	// HookedWork is the display string for hooked work (e.g., "gt-abc123: Fix bug...")
	HookedWork string `json:"hooked_work,omitempty"`

	// MailUnread is the count of unread messages.
	MailUnread int `json:"mail_unread,omitempty"`

	// MailSubject is the subject of the first unread message (truncated).
	MailSubject string `json:"mail_subject,omitempty"`

	// CurrentWork is the display string for in_progress work (fallback when no hook).
	CurrentWork string `json:"current_work,omitempty"`
}

// RigStatus contains status for a single rig.
type RigStatus struct {
	HasWitness  bool   `json:"has_witness"`
	HasRefinery bool   `json:"has_refinery"`
	OpState     string `json:"op_state"` // "OPERATIONAL", "PARKED", "DOCKED"
}

// AgentHealth tracks working/total counts for an agent type.
type AgentHealth struct {
	Total   int `json:"total"`
	Working int `json:"working"`
}

// Cache is the complete status line cache structure.
type Cache struct {
	// UpdatedAt is when this cache was last refreshed.
	UpdatedAt time.Time `json:"updated_at"`

	// Identities maps identity string (e.g., "gastown/nux") to their status data.
	Identities map[string]*IdentityData `json:"identities"`

	// Rigs maps rig name to its status.
	Rigs map[string]*RigStatus `json:"rigs,omitempty"`

	// AgentHealth maps agent type to health stats (for Mayor status line).
	AgentHealth map[string]*AgentHealth `json:"agent_health,omitempty"`

	// HasDeacon indicates if deacon is running.
	HasDeacon bool `json:"has_deacon,omitempty"`

	// RigCount is the total number of active rigs (for Deacon status line).
	RigCount int `json:"rig_count,omitempty"`
}

// CachePath returns the full path to the cache file.
func CachePath(townRoot string) string {
	return filepath.Join(townRoot, "daemon", CacheFile)
}

// LoadCache reads the cache from disk.
// Returns nil if cache doesn't exist or can't be read.
func LoadCache(townRoot string) *Cache {
	path := CachePath(townRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}

	return &cache
}

// Save writes the cache to disk atomically.
func (c *Cache) Save(townRoot string) error {
	path := CachePath(townRoot)

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache: %w", err)
	}

	// Write atomically using temp file + rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp cache file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming cache file: %w", err)
	}

	return nil
}

// IsStale returns true if the cache is too old to use.
func (c *Cache) IsStale() bool {
	if c == nil {
		return true
	}
	return time.Since(c.UpdatedAt) > MaxCacheAge
}

// Age returns how old the cache is.
func (c *Cache) Age() time.Duration {
	if c == nil {
		return MaxCacheAge + time.Hour // Very stale
	}
	return time.Since(c.UpdatedAt)
}

// GetIdentity returns cached data for an identity, or nil if not found.
func (c *Cache) GetIdentity(identity string) *IdentityData {
	if c == nil || c.Identities == nil {
		return nil
	}
	return c.Identities[identity]
}

// SetIdentity sets cached data for an identity.
func (c *Cache) SetIdentity(identity string, data *IdentityData) {
	if c.Identities == nil {
		c.Identities = make(map[string]*IdentityData)
	}
	c.Identities[identity] = data
}

// CacheManager handles periodic cache updates.
type CacheManager struct {
	townRoot string
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	cache    *Cache
	updater  CacheUpdater
}

// CacheUpdater is a function that populates the cache with fresh data.
// It receives the current cache and should update it in place.
type CacheUpdater func(cache *Cache) error

// NewCacheManager creates a new cache manager.
func NewCacheManager(townRoot string, updater CacheUpdater) *CacheManager {
	return &CacheManager{
		townRoot: townRoot,
		interval: DefaultCacheInterval,
		stopCh:   make(chan struct{}),
		updater:  updater,
	}
}

// SetInterval sets the cache refresh interval.
func (m *CacheManager) SetInterval(interval time.Duration) {
	m.interval = interval
}

// Start begins periodic cache updates.
func (m *CacheManager) Start() error {
	// Load existing cache or create new one
	m.mu.Lock()
	m.cache = LoadCache(m.townRoot)
	if m.cache == nil {
		m.cache = &Cache{
			Identities: make(map[string]*IdentityData),
			Rigs:       make(map[string]*RigStatus),
		}
	}
	m.mu.Unlock()

	// Do initial update
	if err := m.update(); err != nil {
		// Non-fatal - continue even if first update fails
		fmt.Fprintf(os.Stderr, "Initial cache update failed: %v\n", err)
	}

	// Start background updater
	m.wg.Add(1)
	go m.runLoop()

	return nil
}

// Stop stops periodic cache updates.
func (m *CacheManager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// runLoop is the background update loop.
func (m *CacheManager) runLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if err := m.update(); err != nil {
				// Log but continue
				fmt.Fprintf(os.Stderr, "Cache update failed: %v\n", err)
			}
		}
	}
}

// update refreshes the cache.
func (m *CacheManager) update() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Call the updater function
	if m.updater != nil {
		if err := m.updater(m.cache); err != nil {
			return err
		}
	}

	// Update timestamp
	m.cache.UpdatedAt = time.Now()

	// Save to disk
	return m.cache.Save(m.townRoot)
}

// GetCache returns the current cache (thread-safe copy reference).
func (m *CacheManager) GetCache() *Cache {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cache
}
