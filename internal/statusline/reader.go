package statusline

// Reader provides read-only access to the status line cache.
// This is used by gt status-line to get cached data.

// GetCachedHookedWork returns cached hooked work for an identity.
// Returns empty string if cache is stale or identity not found.
func GetCachedHookedWork(townRoot, identity string, maxLen int) string {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return "" // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil || data.HookedWork == "" {
		return ""
	}

	// Truncate if needed
	work := data.HookedWork
	if len(work) > maxLen {
		work = work[:maxLen-1] + "\u2026"
	}
	return work
}

// GetCachedMailPreview returns cached mail preview for an identity.
// Returns (0, "") if cache is stale or identity not found.
func GetCachedMailPreview(townRoot, identity string, maxLen int) (int, string) {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return 0, "" // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil || data.MailUnread == 0 {
		return 0, ""
	}

	// Truncate subject if needed
	subject := data.MailSubject
	if len(subject) > maxLen {
		subject = subject[:maxLen-1] + "\u2026"
	}
	return data.MailUnread, subject
}

// GetCachedCurrentWork returns cached current work (in_progress bead) for an identity.
// Returns empty string if cache is stale or identity not found.
func GetCachedCurrentWork(townRoot, identity string, maxLen int) string {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return "" // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil || data.CurrentWork == "" {
		return ""
	}

	// Truncate if needed
	work := data.CurrentWork
	if len(work) > maxLen {
		work = work[:maxLen-1] + "\u2026"
	}
	return work
}

// CacheAvailable returns true if a non-stale cache exists.
func CacheAvailable(townRoot string) bool {
	cache := LoadCache(townRoot)
	return cache != nil && !cache.IsStale()
}

// GetCacheAge returns how old the cache is.
// Returns a very large duration if cache doesn't exist.
func GetCacheAge(townRoot string) (age string, stale bool) {
	cache := LoadCache(townRoot)
	if cache == nil {
		return "not found", true
	}
	return cache.Age().Round(1000000000).String(), cache.IsStale() // Round to seconds
}
