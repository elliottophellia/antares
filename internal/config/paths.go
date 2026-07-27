package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Home is the Antares state directory: ~/.antares (override with ANTARES_HOME).
func Home() string {
	if v := strings.TrimSpace(os.Getenv("ANTARES_HOME")); v != "" {
		return expand(v)
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ".antares"
	}
	return filepath.Join(h, ".antares")
}

// Path joins parts onto the Antares home directory.
func Path(parts ...string) string {
	return filepath.Join(append([]string{Home()}, parts...)...)
}

// EnsureHome creates the state directory tree.
func EnsureHome() error {
	for _, d := range []string{"", "sessions", "logs", "skills", "uploads", "backups", "plugins", "tmp"} {
		if err := os.MkdirAll(Path(d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ConfigFile is the active config path, honouring the selected profile.
func ConfigFile() string {
	if v := strings.TrimSpace(os.Getenv("ANTARES_CONFIG")); v != "" {
		return expand(v)
	}
	if p := ActiveProfile(); p != "" && p != "default" {
		return Path("profiles", p, "config.yaml")
	}
	return Path("config.yaml")
}

// ActiveProfile returns the currently selected config profile name.
func ActiveProfile() string {
	if v := strings.TrimSpace(os.Getenv("ANTARES_PROFILE")); v != "" {
		return v
	}
	b, err := os.ReadFile(Path("active_profile"))
	if err != nil {
		return "default"
	}
	name := strings.TrimSpace(string(b))
	if name == "" {
		return "default"
	}
	return name
}

// SetActiveProfile persists the profile selection.
func SetActiveProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	if err := EnsureHome(); err != nil {
		return err
	}
	return os.WriteFile(Path("active_profile"), []byte(name+"\n"), 0o600)
}

func expand(p string) string {
	// A default like "~/.antares/skills" has to follow ANTARES_HOME, or moving
	// the state directory would move everything except the parts that matter.
	// An explicit path elsewhere still means what it says.
	for _, prefix := range []string{"~/.antares/", "~/.antares"} {
		if strings.HasPrefix(p, prefix) {
			return filepath.Join(Home(), strings.TrimPrefix(strings.TrimPrefix(p, prefix), "/"))
		}
	}
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return os.ExpandEnv(p)
}

// Expand is the exported form of path expansion (~ and $VARS).
func Expand(p string) string { return expand(p) }
