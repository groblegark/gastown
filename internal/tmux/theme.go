package tmux

// Theme defines a color theme for tmux status bars.
// This is a stub retained for API compatibility; themes are no longer applied
// to tmux sessions in the K8s-only architecture.
type Theme struct {
	Name string // Human-readable name
	BG   string // Background color (hex or tmux color name)
	FG   string // Foreground color (hex or tmux color name)
}

// Style returns a human-readable representation of the theme colors.
func (t Theme) Style() string {
	return "bg:" + t.BG + " fg:" + t.FG
}

// DefaultPalette is the curated set of distinct, professional color themes.
var DefaultPalette = []Theme{
	{Name: "ocean", BG: "#1e3a5f", FG: "#e0e0e0"},
	{Name: "forest", BG: "#2d5a3d", FG: "#e0e0e0"},
	{Name: "rust", BG: "#8b4513", FG: "#f5f5dc"},
	{Name: "plum", BG: "#4a3050", FG: "#e0e0e0"},
	{Name: "slate", BG: "#4a5568", FG: "#e0e0e0"},
	{Name: "ember", BG: "#b33a00", FG: "#f5f5dc"},
	{Name: "midnight", BG: "#1a1a2e", FG: "#c0c0c0"},
	{Name: "wine", BG: "#722f37", FG: "#f5f5dc"},
	{Name: "teal", BG: "#0d5c63", FG: "#e0e0e0"},
	{Name: "copper", BG: "#6d4c41", FG: "#f5f5dc"},
}

// MayorTheme returns the theme for the mayor agent.
func MayorTheme() Theme {
	return Theme{Name: "mayor", BG: "#b33a00", FG: "#f5f5dc"}
}

// DeaconTheme returns the theme for the deacon agent.
func DeaconTheme() Theme {
	return Theme{Name: "deacon", BG: "#4a3050", FG: "#e0e0e0"}
}

// DogTheme returns the theme for the dog agent.
func DogTheme() Theme {
	return Theme{Name: "dog", BG: "#4a5568", FG: "#e0e0e0"}
}

// GetThemeByName returns a theme by name, or nil if not found.
func GetThemeByName(name string) *Theme {
	for _, t := range DefaultPalette {
		if t.Name == name {
			return &t
		}
	}
	// Check named agent themes
	switch name {
	case "mayor":
		th := MayorTheme()
		return &th
	case "deacon":
		th := DeaconTheme()
		return &th
	case "dog":
		th := DogTheme()
		return &th
	}
	return nil
}

// AssignTheme assigns a theme to a rig based on its name.
func AssignTheme(rigName string) Theme {
	return AssignThemeFromPalette(rigName, DefaultPalette)
}

// AssignThemeFromPalette assigns a theme from a palette based on rig name hash.
func AssignThemeFromPalette(rigName string, palette []Theme) Theme {
	if len(palette) == 0 {
		return Theme{Name: "default", BG: "#1e3a5f", FG: "#e0e0e0"}
	}
	// Simple hash to deterministically assign themes
	h := 0
	for _, c := range rigName {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return palette[h%len(palette)]
}

// ListThemeNames returns the names of all available themes.
func ListThemeNames() []string {
	names := make([]string, len(DefaultPalette))
	for i, t := range DefaultPalette {
		names[i] = t.Name
	}
	return names
}
