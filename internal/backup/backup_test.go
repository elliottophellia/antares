package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed builds a state directory that looks like a real one.
func seed(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		"config.yaml":           "model:\n  default: x\n",
		"antares.db":            "not really sqlite, but a file",
		"skills/deploy.md":      "---\nname: deploy\n---\n",
		"plugins/a/plugin.yaml": "name: a\ncommand: ./run.sh\n",
		// These should not be kept.
		"antares.db-wal":     "write ahead",
		"antares.db-shm":     "shared memory",
		"tmp/scratch":        "junk",
		"browser/Cookies":    "session cookies",
		"checkpoints/s1/x":   "old copy",
		"backups/old.tar.gz": "an earlier backup",
	}
	for rel, body := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// names lists what an archive holds.
func names(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	var out []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		out = append(out, hdr.Name)
	}
	return out
}

func TestCreateKeepsWhatMattersAndSkipsTheRest(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()

	info, err := Create(home, dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size == 0 {
		t.Fatal("the archive is empty")
	}

	held := strings.Join(names(t, info.Path), "\n")
	for _, want := range []string{"config.yaml", "antares.db", "skills/deploy.md", "antares-backup.json"} {
		if !strings.Contains(held, want) {
			t.Errorf("the archive is missing %s:\n%s", want, held)
		}
	}
	for _, unwanted := range []string{"db-wal", "db-shm", "tmp/", "browser/", "checkpoints/", "backups/"} {
		if strings.Contains(held, unwanted) {
			t.Errorf("the archive kept %s, which regenerates:\n%s", unwanted, held)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()
	info, err := Create(home, dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	restored := t.TempDir()
	n, err := Restore(info.Path, restored, false)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing was restored")
	}
	got, err := os.ReadFile(filepath.Join(restored, "skills", "deploy.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "name: deploy") {
		t.Fatalf("the restored file is %q", got)
	}
}

func TestRestoreRefusesToOverwriteWithoutForce(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()
	info, err := Create(home, dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Restoring onto a directory that already holds a database would replace
	// the state a running instance is using.
	if _, err := Restore(info.Path, home, false); err == nil {
		t.Fatal("expected a refusal")
	}
	if _, err := Restore(info.Path, home, true); err != nil {
		t.Fatalf("force should allow it: %v", err)
	}
}

func TestRestoreRejectsPathsOutsideTheDirectory(t *testing.T) {
	// A crafted archive naming ../../etc/passwd must not write there.
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.tar.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("owned")
	_ = tw.WriteHeader(&tar.Header{
		Name: "../../escaped.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	into := t.TempDir()
	if _, err := Restore(path, into, true); err == nil {
		t.Fatal("a path traversal was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(into)), "escaped.txt")); err == nil {
		t.Fatal("a file was written outside the restore directory")
	}
}

func TestRestoreRejectsSomethingThatIsNotAnArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notreally.tar.gz")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(path, t.TempDir(), true); err == nil {
		t.Fatal("expected an error for a file that is not an archive")
	}
}

func TestQuiesceFailureStopsTheBackup(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()
	// A backup taken from an unsettled database is worse than no backup,
	// because it looks like one.
	_, err := Create(home, dir, "test", nil, func() error { return os.ErrPermission })
	if err == nil {
		t.Fatal("expected the backup to fail when the database could not settle")
	}
	list, _ := List(dir)
	if len(list) != 0 {
		t.Fatalf("a failed backup left %d archive(s) behind", len(list))
	}
}

func TestListAndPrune(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()

	// Names carry a timestamp to the second, so write them by hand to get
	// several without waiting.
	for _, name := range []string{"antares-a.tar.gz", "antares-b.tar.gz", "antares-c.tar.gz"} {
		info, err := Create(home, dir, "test", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(info.Path, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("listed %d archives", len(list))
	}

	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("pruned %d", removed)
	}
	if after, _ := List(dir); len(after) != 2 {
		t.Fatalf("%d archives left", len(after))
	}
}

func TestListOnAMissingDirectory(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("a missing directory should not be an error: %v", err)
	}
	if len(list) != 0 {
		t.Fatal("got archives from a directory that does not exist")
	}
}

func TestExternalDatabaseIsIncludedAndRestored(t *testing.T) {
	home := seed(t)
	// A DSN pointing outside the state directory is common: the database lives
	// on another volume. Omitting it would make the backup worthless.
	elsewhere := t.TempDir()
	dbPath := filepath.Join(elsewhere, "somewhere.db")
	if err := os.WriteFile(dbPath, []byte("the real database"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	info, err := Create(home, dir, "test", []string{dbPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	held := strings.Join(names(t, info.Path), "\n")
	if !strings.Contains(held, "antares-external/") {
		t.Fatalf("the external database was not archived:\n%s", held)
	}

	// Restoring puts it back where it came from.
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(info.Path, t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("the external database was not restored: %v", err)
	}
	if string(got) != "the real database" {
		t.Fatalf("restored %q", got)
	}
}

func TestExternalRestoreRefusesToOverwriteWithoutForce(t *testing.T) {
	home := seed(t)
	elsewhere := t.TempDir()
	dbPath := filepath.Join(elsewhere, "somewhere.db")
	if err := os.WriteFile(dbPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	info, err := Create(home, dir, "test", []string{dbPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The file still exists, so restoring over it needs saying so twice.
	if _, err := Restore(info.Path, t.TempDir(), false); err == nil {
		t.Fatal("expected a refusal to overwrite a database outside the state directory")
	}
	if got, _ := os.ReadFile(dbPath); string(got) != "newer" {
		t.Fatal("the file was overwritten despite the refusal")
	}
}

func TestFilesAlreadyInsideHomeAreNotArchivedTwice(t *testing.T) {
	home := seed(t)
	dir := t.TempDir()
	inside := filepath.Join(home, "antares.db")

	info, err := Create(home, dir, "test", []string{inside}, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range names(t, info.Path) {
		if strings.HasSuffix(n, "antares.db") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("antares.db appears %d times in the archive", count)
	}
}
