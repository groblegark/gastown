package statusline

// Reader provides read-only access to the status line cache.
// This is used by gt status-line to get cached data.

// GetCachedHookedWork returns cached hooked work for an identity.
// Returns (work, cacheHit) where:
//   - cacheHit=false: cache stale or identity not found, caller should query directly
//   - cacheHit=true, work="": identity found in cache but has no hooked work
//   - cacheHit=true, work="...": identity found with hooked work
func GetCachedHookedWork(townRoot, identity string, maxLen int) (string, bool) {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return "", false // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil {
		return "", false // Identity not in cache - fall back to direct query
	}

	// Cache hit - identity is in cache (even if no hooked work)
	if data.HookedWork == "" {
		return "", true // Valid cache hit: no hooked work
	}

	// Truncate if needed
	work := data.HookedWork
	if len(work) > maxLen {
		work = work[:maxLen-1] + "\u2026"
	}
	return work, true
}

// GetCachedMailPreview returns cached mail preview for an identity.
// Returns (count, subject, cacheHit) where:
//   - cacheHit=false: cache stale or identity not found, caller should query directly
//   - cacheHit=true, count=0: identity found in cache but has no unread mail
//   - cacheHit=true, count>0: identity found with unread mail
func GetCachedMailPreview(townRoot, identity string, maxLen int) (int, string, bool) {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return 0, "", false // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil {
		return 0, "", false // Identity not in cache - fall back to direct query
	}

	// Cache hit - identity is in cache (even if no unread mail)
	if data.MailUnread == 0 {
		return 0, "", true // Valid cache hit: no unread mail
	}

	// Truncate subject if needed
	subject := data.MailSubject
	if len(subject) > maxLen {
		subject = subject[:maxLen-1] + "\u2026"
	}
	return data.MailUnread, subject, true
}

// GetCachedCurrentWork returns cached current work (in_progress bead) for an identity.
// Returns (work, cacheHit) where:
//   - cacheHit=false: cache stale or identity not found, caller should query directly
//   - cacheHit=true, work="": identity found in cache but has no current work
//   - cacheHit=true, work="...": identity found with current work
func GetCachedCurrentWork(townRoot, identity string, maxLen int) (string, bool) {
	cache := LoadCache(townRoot)
	if cache == nil || cache.IsStale() {
		return "", false // Stale cache - signal to fall back to direct query
	}

	data := cache.GetIdentity(identity)
	if data == nil {
		return "", false // Identity not in cache - fall back to direct query
	}

	// Cache hit - identity is in cache (even if no current work)
	if data.CurrentWork == "" {
		return "", true // Valid cache hit: no current work
	}

	// Truncate if needed
	work := data.CurrentWork
	if len(work) > maxLen {
		work = work[:maxLen-1] + "\u2026"
	}
	return work, true
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
