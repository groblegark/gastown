package sling

import (
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
)

// IsRigName checks if a target string is a rig name.
// Returns the rig name and true if it's a valid rig.
func IsRigName(target, townRoot string) (string, bool) {
	if strings.Contains(target, "/") {
		return "", false
	}

	switch strings.ToLower(target) {
	case "mayor", "may", "deacon", "dea", "crew", "witness", "wit", "refinery", "ref":
		return "", false
	}

	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return "", false
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	_, err = rigMgr.GetRig(target)
	if err != nil {
		return "", false
	}

	return target, true
}

// IsDogTarget checks if target is a dog target pattern.
// Returns the dog name (or empty for pool dispatch) and true if it's a dog target.
func IsDogTarget(target string) (dogName string, isDog bool) {
	t := strings.ToLower(target)

	if t == "deacon/dogs" || t == "dog:" {
		return "", true
	}
	if strings.HasPrefix(t, "dog:") {
		name := strings.TrimPrefix(t, "dog:")
		if name != "" && !strings.Contains(name, "/") {
			return name, true
		}
		return "", true
	}
	if strings.HasPrefix(t, "deacon/dogs/") {
		name := strings.TrimPrefix(t, "deacon/dogs/")
		if name != "" && !strings.Contains(name, "/") {
			return name, true
		}
	}
	return "", false
}

// ParseCrewTarget parses a crew target string and returns the rig name and crew name.
// Returns (rigName, crewName, true) if the target is a valid crew target format "rig/crew/name".
func ParseCrewTarget(target string) (rigName, crewName string, ok bool) {
	parts := strings.Split(target, "/")
	if len(parts) == 3 && parts[1] == "crew" {
		return parts[0], parts[2], true
	}
	return "", "", false
}

// IsPolecatTarget checks if the target string refers to a polecat.
func IsPolecatTarget(target string) bool {
	parts := strings.Split(target, "/")
	return len(parts) >= 3 && parts[1] == "polecats"
}
