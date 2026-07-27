package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandFollowsAntaresHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANTARES_HOME", dir)

	// The defaults are written as ~/.antares/... and must land inside the
	// configured home, or relocating it would move the database and leave the
	// skills and plugins behind.
	cases := map[string]string{
		"~/.antares/skills":  filepath.Join(dir, "skills"),
		"~/.antares/plugins": filepath.Join(dir, "plugins"),
		"~/.antares":         dir,
		"~/.antares/browser": filepath.Join(dir, "browser"),
	}
	for in, want := range cases {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandLeavesOtherPathsAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANTARES_HOME", dir)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// An explicit path outside the state directory still means what it says.
	if got, want := Expand("~/projects"), filepath.Join(home, "projects"); got != want {
		t.Errorf("Expand(~/projects) = %q, want %q", got, want)
	}
	if got := Expand("/etc/antares"); got != "/etc/antares" {
		t.Errorf("an absolute path was rewritten to %q", got)
	}
}

func TestExpandWithoutAntaresHome(t *testing.T) {
	t.Setenv("ANTARES_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := Expand("~/.antares/skills"), filepath.Join(home, ".antares", "skills"); got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}
