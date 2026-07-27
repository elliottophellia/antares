package hub

import (
	"os"
	"path/filepath"
)

// Seed writes the bundled skills into dir the first time Antares runs, so a
// fresh install has something to work with rather than an empty library.
//
// It only ever runs once — the marker file is what stops a skill the user
// deleted on purpose from reappearing on the next start. Skills that already
// exist are left alone, since a user's edit outranks the shipped copy.
func Seed(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	marker := filepath.Join(dir, ".seeded")
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	written := 0
	for _, e := range BundledSkills() {
		path := filepath.Join(dir, safeFileName(e.Name)+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(e.Body), 0o644); err != nil {
			return written, err
		}
		written++
	}
	// Written last: a crash midway through means the next start finishes the job.
	if err := os.WriteFile(marker, []byte("Antares wrote the bundled skills here once.\nDelete this file to have them restored.\n"), 0o644); err != nil {
		return written, err
	}
	return written, nil
}
