// Package backup copies everything Antares keeps into one file, and puts it
// back.
//
// Everything worth keeping is in one directory, so a backup is a tarball of
// that directory minus the parts that regenerate themselves. The point of
// having it in-process rather than telling people to run tar is that the
// database has to be quiesced first — copying a SQLite file mid-write produces
// an archive that restores into a corrupt database.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Info describes one archive.
type Info struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// externalPrefix marks entries that came from outside the state directory.
const externalPrefix = "antares-external/"

// manifest is written into every archive so a restore knows what it holds.
type manifest struct {
	CreatedAt time.Time `json:"created_at"`
	Version   string    `json:"version"`
	Files     int       `json:"files"`
}

// skipped names what is not worth keeping: caches, temporary files, and the
// SQLite sidecars, which are meaningless without the moment they belong to.
var skipped = []string{
	"tmp/", "backups/", "browser/", "screenshots/", "exports/", "checkpoints/",
	".db-wal", ".db-shm", ".seeded",
}

func shouldSkip(rel string) bool {
	for _, s := range skipped {
		if strings.HasPrefix(rel, s) || strings.HasSuffix(rel, s) {
			return true
		}
	}
	return false
}

// Create writes a gzipped tar of home into dir and returns what it wrote.
//
// extra names files outside the state directory that belong in the backup —
// in practice a SQLite database whose DSN points somewhere else. Without this
// the archive would silently omit the one thing it exists to protect.
//
// quiesce is called before reading anything and is where the caller
// checkpoints the database; without it the archive can hold a torn file.
func Create(home, dir, version string, extra []string, quiesce func() error) (Info, error) {
	var info Info
	if home == "" {
		return info, errors.New("no state directory to back up")
	}
	if quiesce != nil {
		if err := quiesce(); err != nil {
			return info, fmt.Errorf("could not settle the database first: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return info, err
	}

	name := fmt.Sprintf("antares-%s.tar.gz", time.Now().Format("2006-01-02-150405"))
	path := filepath.Join(dir, name)

	// Write to a temporary name and rename at the end, so an interrupted
	// backup never looks like a complete one.
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return info, err
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	count := 0
	err = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // an unreadable file is not worth failing the whole backup
		}
		rel, err := filepath.Rel(home, p)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if shouldSkip(rel) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() || !fi.Mode().IsRegular() {
			return nil
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return nil
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer src.Close()
		if _, err := io.Copy(tw, src); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return info, err
	}

	// Anything outside the state directory goes under a prefix that says so,
	// and is restored to the same absolute path it came from.
	for _, p := range extra {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(home, abs); err == nil && !strings.HasPrefix(rel, "..") {
			continue // already inside, already archived
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			continue
		}
		hdr.Name = externalPrefix + filepath.ToSlash(strings.TrimPrefix(abs, "/"))
		if err := tw.WriteHeader(hdr); err != nil {
			return info, err
		}
		src, err := os.Open(abs)
		if err != nil {
			continue
		}
		_, copyErr := io.Copy(tw, src)
		src.Close()
		if copyErr != nil {
			return info, copyErr
		}
		count++
	}

	meta, _ := json.Marshal(manifest{CreatedAt: time.Now(), Version: version, Files: count})
	if err := tw.WriteHeader(&tar.Header{
		Name: "antares-backup.json", Mode: 0o600, Size: int64(len(meta)),
	}); err != nil {
		return info, err
	}
	if _, err := tw.Write(meta); err != nil {
		return info, err
	}

	if err := tw.Close(); err != nil {
		return info, err
	}
	if err := gz.Close(); err != nil {
		return info, err
	}
	if err := f.Close(); err != nil {
		return info, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return info, err
	}

	stat, err := os.Stat(path)
	if err != nil {
		return info, err
	}
	return Info{Path: path, Name: name, Size: stat.Size(), CreatedAt: stat.ModTime()}, nil
}

// List reports the archives in a directory, newest first.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{
			Path: filepath.Join(dir, e.Name()), Name: e.Name(),
			Size: fi.Size(), CreatedAt: fi.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Restore unpacks an archive over home.
//
// It refuses to overwrite a directory that already holds a database unless
// force is set: restoring onto a running instance would replace the very state
// it is using, which is almost never what someone means.
func Restore(archive, home string, force bool) (int, error) {
	if !force {
		if _, err := os.Stat(filepath.Join(home, "antares.db")); err == nil {
			return 0, errors.New("there is already a database here — stop Antares and pass force to replace it")
		}
	}
	f, err := os.Open(archive)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("this does not look like a backup: %w", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(home, 0o755); err != nil {
		return 0, err
	}

	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// An archive is untrusted input: a name containing .. would write
		// outside the directory being restored into.
		clean := filepath.Clean(filepath.Join(home, filepath.FromSlash(hdr.Name)))
		if !strings.HasPrefix(hdr.Name, externalPrefix) &&
			!strings.HasPrefix(clean, filepath.Clean(home)+string(os.PathSeparator)) {
			return count, fmt.Errorf("refusing to restore %q, which points outside the directory", hdr.Name)
		}
		if hdr.Name == "antares-backup.json" {
			continue
		}
		// A file archived from outside the state directory goes back to the
		// absolute path it came from, not into the restore directory.
		if strings.HasPrefix(hdr.Name, externalPrefix) {
			target := "/" + strings.TrimPrefix(hdr.Name, externalPrefix)
			if !force {
				if _, err := os.Stat(target); err == nil {
					return count, fmt.Errorf("%s already exists — pass force to replace it", target)
				}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return count, err
			}
			_, copyErr := io.Copy(out, io.LimitReader(tr, 1<<30))
			out.Close()
			if copyErr != nil {
				return count, copyErr
			}
			count++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return count, err
		}
		out, err := os.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
		if err != nil {
			return count, err
		}
		// Bounded, so a crafted archive cannot fill the disk in one entry.
		if _, err := io.Copy(out, io.LimitReader(tr, 1<<30)); err != nil {
			out.Close()
			return count, err
		}
		out.Close()
		count++
	}
	return count, nil
}

// Prune keeps the newest n archives and deletes the rest.
func Prune(dir string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	list, err := List(dir)
	if err != nil || len(list) <= keep {
		return 0, err
	}
	removed := 0
	for _, info := range list[keep:] {
		if os.Remove(info.Path) == nil {
			removed++
		}
	}
	return removed, nil
}
