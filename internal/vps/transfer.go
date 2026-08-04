package vps

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// MaxTransferBytes caps a single upload or download so a runaway path cannot
// exhaust memory or fill the workspace. 256 MiB is enough for configs, modest
// builds, and logs; larger payloads should use a remote pull (curl/wget) or
// rsync via vps_run.
const MaxTransferBytes = 256 << 20

// Upload copies a local file to remotePath on the VPS over SFTP. Parent
// directories on the remote side are created as needed. Returns bytes written
// and the host key seen (for TOFU pinning).
func Upload(ctx context.Context, t Target, localPath, remotePath string) (n int64, seen string, err error) {
	localPath = filepath.Clean(localPath)
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "" {
		return 0, "", fmt.Errorf("remote_path is required")
	}
	fi, err := os.Stat(localPath)
	if err != nil {
		return 0, "", fmt.Errorf("local file: %w", err)
	}
	if fi.IsDir() {
		return 0, "", fmt.Errorf("local path is a directory; upload a single file")
	}
	if fi.Size() > MaxTransferBytes {
		return 0, "", fmt.Errorf("local file is %d bytes; max is %d — use a remote pull for larger payloads", fi.Size(), MaxTransferBytes)
	}

	client, err := dial(ctx, t)
	if err != nil {
		return 0, "", err
	}
	defer client.Close()
	seen = client.seenHostKey

	sc, err := sftp.NewClient(client.Client)
	if err != nil {
		return 0, seen, fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	if dir := path.Dir(remotePath); dir != "" && dir != "." {
		if err := sc.MkdirAll(dir); err != nil {
			return 0, seen, fmt.Errorf("create remote dir %s: %w", dir, err)
		}
	}

	src, err := os.Open(localPath)
	if err != nil {
		return 0, seen, err
	}
	defer src.Close()

	// Upload to a sibling temp path and rename over the target, so a transfer that
	// times out partway cannot leave a truncated file where a working one was.
	// That matters most for the usual reason to upload: replacing a live service
	// config the next `systemctl restart` will read.
	tmpRemote := remotePath + ".antares-upload.tmp"
	dst, err := sc.OpenFile(tmpRemote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, seen, fmt.Errorf("open remote %s: %w", tmpRemote, err)
	}
	committed := false
	defer func() {
		_ = dst.Close()
		if !committed {
			_ = sc.Remove(tmpRemote)
		}
	}()

	n, err = copyWithContext(ctx, dst, src)
	if err != nil {
		return n, seen, err
	}
	if err := dst.Close(); err != nil {
		return n, seen, fmt.Errorf("close remote %s: %w", tmpRemote, err)
	}
	// Preserve mode bits the local file had (best-effort; some servers refuse).
	_ = sc.Chmod(tmpRemote, fi.Mode().Perm())
	// Some SFTP servers refuse a rename onto an existing path; fall back to
	// removing the target first, which is still narrower than truncate-then-write.
	if err := sc.Rename(tmpRemote, remotePath); err != nil {
		_ = sc.Remove(remotePath)
		if err2 := sc.Rename(tmpRemote, remotePath); err2 != nil {
			return n, seen, fmt.Errorf("finalise remote %s: %w", remotePath, err2)
		}
	}
	committed = true
	return n, seen, nil
}

// Download copies remotePath from the VPS into localPath over SFTP. Parent
// directories on the local side are created as needed. Returns bytes written
// and the host key seen.
func Download(ctx context.Context, t Target, remotePath, localPath string) (n int64, seen string, err error) {
	localPath = filepath.Clean(localPath)
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "" {
		return 0, "", fmt.Errorf("remote_path is required")
	}

	client, err := dial(ctx, t)
	if err != nil {
		return 0, "", err
	}
	defer client.Close()
	seen = client.seenHostKey

	sc, err := sftp.NewClient(client.Client)
	if err != nil {
		return 0, seen, fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	fi, err := sc.Stat(remotePath)
	if err != nil {
		return 0, seen, fmt.Errorf("remote file: %w", err)
	}
	if fi.IsDir() {
		return 0, seen, fmt.Errorf("remote path is a directory; download a single file")
	}
	if fi.Size() > MaxTransferBytes {
		return 0, seen, fmt.Errorf("remote file is %d bytes; max is %d", fi.Size(), MaxTransferBytes)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, seen, fmt.Errorf("create local dir: %w", err)
	}

	src, err := sc.Open(remotePath)
	if err != nil {
		return 0, seen, fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer src.Close()

	// Stream into a sibling temp file and rename on success. Writing straight to
	// localPath with O_TRUNC would destroy an existing file before the first byte
	// arrives, so a timeout mid-transfer (the common case on a slow link) would
	// leave a truncated stub where the original used to be.
	//
	// The mode is a fixed 0o600 rather than the remote file's bits: a remote file
	// that happens to be 0777 must not produce a world-writable local file, and a
	// remote 0400 must not produce one the user cannot read.
	dst, err := os.CreateTemp(filepath.Dir(localPath), ".antares-download-*")
	if err != nil {
		return 0, seen, fmt.Errorf("create local temp file: %w", err)
	}
	tmpName := dst.Name()
	committed := false
	defer func() {
		_ = dst.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := dst.Chmod(0o600); err != nil {
		return 0, seen, fmt.Errorf("chmod local temp file: %w", err)
	}

	n, err = copyWithContext(ctx, dst, src)
	if err != nil {
		return n, seen, err
	}
	if err := dst.Close(); err != nil {
		return n, seen, fmt.Errorf("close local file: %w", err)
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		return n, seen, fmt.Errorf("finalise local file: %w", err)
	}
	committed = true
	return n, seen, nil
}

// cleanRemotePath normalises a remote path for SFTP (slash-separated). Absolute
// paths stay absolute; relative paths stay relative so they resolve under the
// SSH user's home when the server supports it.
func cleanRemotePath(orig string) string {
	orig = strings.TrimSpace(orig)
	if orig == "" {
		return ""
	}
	orig = filepath.ToSlash(orig)
	abs := strings.HasPrefix(orig, "/")
	cleaned := path.Clean(orig)
	if cleaned == "." {
		return ""
	}
	if abs && !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// copyWithContext streams src to dst, aborting if ctx is cancelled, and
// enforcing MaxTransferBytes. Reads run in a goroutine so a blocked Read does
// not ignore cancellation (plain io.Copy cannot be cancelled).
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	type readRes struct {
		n   int
		err error
	}
	for {
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		ch := make(chan readRes, 1)
		go func() {
			n, err := src.Read(buf)
			ch <- readRes{n: n, err: err}
		}()
		var rr readRes
		select {
		case <-ctx.Done():
			return written, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
		case rr = <-ch:
		}
		if rr.n > 0 {
			if written+int64(rr.n) > MaxTransferBytes {
				return written, fmt.Errorf("transfer exceeds max of %d bytes", MaxTransferBytes)
			}
			nw, ew := dst.Write(buf[0:rr.n])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if rr.n != nw {
				return written, io.ErrShortWrite
			}
		}
		if rr.err == io.EOF {
			return written, nil
		}
		if rr.err != nil {
			return written, rr.err
		}
	}
}
