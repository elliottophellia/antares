// Package skillpack ships a large library of security methodology skills inside
// the binary and unpacks it on first run.
//
// These are the testing procedures a security engagement draws on — thousands
// of them, covering web, API, cloud, infrastructure, and compliance. They are
// not injected into every prompt: that would bury the conversation. They are
// installed to disk and found on demand, the way a reference is used — the
// security roles search for the technique they need and pull it in.
package skillpack

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed pack.tar.gz
var packFS embed.FS

// marker records that the pack has been unpacked, so it happens once. A user
// who deletes a skill from the pack does not get it back on the next start.
const marker = ".security-pack-installed"

// Install unpacks the security skill library into dir the first time. It
// returns how many files it wrote. A pack that is already installed writes
// nothing.
func Install(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	raw, err := packFS.Open("pack.tar.gz")
	if err != nil {
		return 0, err
	}
	defer raw.Close()

	gz, err := gzip.NewReader(raw)
	if err != nil {
		return 0, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	written := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return written, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The archive is our own, but treat it as untrusted anyway: a name
		// with .. must not escape the target directory.
		clean := filepath.Clean(filepath.Join(dir, filepath.FromSlash(hdr.Name)))
		if !strings.HasPrefix(clean, filepath.Clean(dir)+string(os.PathSeparator)) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return written, err
		}
		out, err := os.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return written, err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 4<<20)); err != nil {
			out.Close()
			return written, err
		}
		out.Close()
		written++
	}

	_ = os.WriteFile(filepath.Join(dir, marker),
		[]byte("Antares unpacked the security skill library here once.\nDelete this file to restore it.\n"), 0o644)
	return written, nil
}
