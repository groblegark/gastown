// Package slack provides Slack integration for Gas Town.
package slack

import (
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// Router resolves agent identities to Slack channel IDs.
type Router struct {
	config   *config.SlackConfig
	patterns []compiledPattern
}

// compiledPattern represents a pre-processed channel routing pattern.
type compiledPattern struct {
	pattern   string   // original pattern string
	channel   string   // target channel ID
	segments  []string // split pattern parts
	wildcards int      // count of "*" segments
}

// NewRouter creates a new Router from the given SlackConfig.
// Patterns are compiled and sorted by specificity for efficient matching.
func NewRouter(cfg *config.SlackConfig) *Router {
	if cfg == nil {
		cfg = config.NewSlackConfig()
	}
	r := &Router{config: cfg}
	r.compilePatterns()
	return r
}

// ResolveChannel returns the Slack channel ID for the given agent identity.
// It tries patterns in specificity order and falls back to DefaultChannel.
func (r *Router) ResolveChannel(agentID string) string {
	if agentID == "" {
		return r.config.DefaultChannel
	}

	agentParts := strings.Split(agentID, "/")

	for _, p := range r.patterns {
		if r.matches(agentParts, p) {
			return p.channel
		}
	}

	return r.config.DefaultChannel
}

// compilePatterns processes all channel patterns and sorts them by specificity.
func (r *Router) compilePatterns() {
	r.patterns = make([]compiledPattern, 0, len(r.config.Channels))

	for pattern, channel := range r.config.Channels {
		segments := strings.Split(pattern, "/")
		wildcards := 0
		for _, seg := range segments {
			if seg == "*" {
				wildcards++
			}
		}
		r.patterns = append(r.patterns, compiledPattern{
			pattern:   pattern,
			channel:   channel,
			segments:  segments,
			wildcards: wildcards,
		})
	}

	// Sort by specificity:
	// 1. Fewer wildcards = higher priority
	// 2. More segments = higher priority (within same wildcard count)
	// 3. Alphabetical by pattern (for stable ordering)
	sort.Slice(r.patterns, func(i, j int) bool {
		pi, pj := r.patterns[i], r.patterns[j]

		// Fewer wildcards wins
		if pi.wildcards != pj.wildcards {
			return pi.wildcards < pj.wildcards
		}

		// More segments wins (more specific)
		if len(pi.segments) != len(pj.segments) {
			return len(pi.segments) > len(pj.segments)
		}

		// Alphabetical for stability
		return pi.pattern < pj.pattern
	})
}

// matches checks if an agent matches a compiled pattern.
func (r *Router) matches(agentParts []string, p compiledPattern) bool {
	// Must have same number of segments
	if len(agentParts) != len(p.segments) {
		return false
	}

	for i, seg := range p.segments {
		if seg != "*" && seg != agentParts[i] {
			return false
		}
	}

	return true
}
