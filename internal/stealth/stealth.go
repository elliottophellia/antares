// Package stealth resolves and launches a source-patched stealth Chromium so
// the browser tool can reach sites guarded by bot-detection challenges
// (Cloudflare Turnstile and the like).
//
// A normal headless Chrome announces itself: navigator.webdriver is true, the
// canvas/WebGL/audio fingerprints are the stock ones, and challenge pages spot
// it in milliseconds. The stealth build is a Chromium compiled to lie
// convincingly — webdriver reads false, the fingerprint surfaces are seeded to
// look like an ordinary machine, and the platform can be spoofed. Antares does
// not ship that binary; it downloads a signed, checksum-verified copy on first
// use (Ed25519 over SHA256SUMS, refusing anything unverified) and caches it.
//
// The downloaded Chromium is covered by its own upstream license, separate from
// Antares. Set ANTARES_STEALTH_BINARY (or CLOAKBROWSER_BINARY_PATH) to point at
// a binary you already have and the download is skipped entirely.
package stealth

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors, wrappable with %w for context.
var (
	ErrUnsupportedPlatform = errors.New("stealth: unsupported platform")
	ErrInvalidVersion      = errors.New("stealth: invalid version pin")
	ErrBinaryNotFound      = errors.New("stealth: binary not found")
	ErrDownload            = errors.New("stealth: download failed")
	ErrVerification        = errors.New("stealth: integrity verification failed")
	ErrExtraction          = errors.New("stealth: archive extraction failed")
	ErrPathTraversal       = errors.New("stealth: path traversal detected in archive")
)

const (
	chunkSize       = 8192
	manifestTimeout = 10 * time.Second

	// chromiumVersion is the latest across all platforms (display/fallback).
	chromiumVersion = "146.0.7680.177.5"

	defaultDownloadBaseURL = "https://cloakbrowser.dev"
	githubDownloadBaseURL  = "https://github.com/CloakHQ/cloakbrowser/releases/download"
)

// signingPubkeys are base64-encoded 32-byte raw Ed25519 public keys that sign
// the SHA256SUMS manifest. The signature is the trust root: a download whose
// manifest no pinned key validates is refused.
var signingPubkeys = []string{"MKFKwIhUcKWq5xTuNA0Ovg99njcDEcEJvmWYYhApvaU="}

// platformVersions pins each platform tag to its published Chromium version.
var platformVersions = map[string]string{
	"linux-x64":    "146.0.7680.177.5",
	"linux-arm64":  "146.0.7680.177.3",
	"darwin-arm64": "145.0.7632.109.2",
	"darwin-x64":   "145.0.7632.109.2",
	"windows-x64":  "146.0.7680.177.5",
}

var versionPinRe = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){3,4}$`)

// env reads an Antares-native variable first, then the upstream fallback so a
// binary already downloaded under ~/.cloakbrowser is picked up unchanged.
func env(antares, fallback string) string {
	if v := os.Getenv(antares); v != "" {
		return v
	}
	return os.Getenv(fallback)
}

// Options configure a stealth launch.
type Options struct {
	// Version pins a specific binary version ("" = platform default).
	Version string
	// Proxy routes traffic through a proxy, e.g. "http://host:3128" or
	// "socks5://host:1080". Credentials embedded in the URL are honoured.
	Proxy string
	// Timezone / Locale override the fingerprint timezone/locale, e.g.
	// "America/New_York" / "en-US". Empty leaves the binary's default.
	Timezone string
	Locale   string
	// Extra appends raw flags after the stealth defaults.
	Extra []string
}

// Args builds the command-line flags for a stealth launch: the fingerprint
// seed and platform spoof, plus optional proxy/timezone/locale. It does NOT
// include --remote-debugging-port, --user-data-dir, --headless, or the window
// size — the caller owns those, exactly as for a normal Chrome launch.
func (o Options) Args() []string {
	seed := rand.Intn(90000) + 10000 // 10000..=99999
	args := []string{
		"--no-sandbox",
		"--fingerprint=" + strconv.Itoa(seed),
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "--fingerprint-platform=macos")
	} else {
		// The stealth build spoofs Windows everywhere but macOS by default;
		// it is the least surprising fingerprint on a server.
		args = append(args, "--fingerprint-platform=windows")
	}
	if tz := strings.TrimSpace(o.Timezone); tz != "" {
		args = append(args, "--fingerprint-timezone="+tz)
	}
	if loc := strings.TrimSpace(o.Locale); loc != "" {
		args = append(args, "--fingerprint-locale="+loc, "--lang="+loc)
	}
	if p := proxyServerArg(o.Proxy); p != "" {
		args = append(args, p)
	}
	if len(o.Extra) > 0 {
		args = append(args, o.Extra...)
	}
	return args
}

// proxyServerArg turns a proxy URL into a --proxy-server flag. Chrome does not
// accept inline credentials on that flag, so any userinfo is stripped here;
// authenticated proxies still route, but the auth prompt is handled at the CDP
// layer by the browser session. Returns "" for an empty or unparseable URL.
func proxyServerArg(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	return "--proxy-server=" + scheme + "://" + u.Host
}

// EnsureBinary returns the path to the stealth Chromium, downloading and
// verifying it on first use. Set ANTARES_STEALTH_BINARY (or the upstream
// CLOAKBROWSER_BINARY_PATH) to skip the download and use a local file.
func EnsureBinary(ctx context.Context, version string) (string, error) {
	if override := env("ANTARES_STEALTH_BINARY", "CLOAKBROWSER_BINARY_PATH"); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%w: stealth binary override set to %q but the file does not exist", ErrBinaryNotFound, override)
		}
		return override, nil
	}

	requested, err := normalizeVersion(version)
	if err != nil {
		return "", err
	}
	if err := checkPlatform(); err != nil {
		return "", err
	}

	if requested != "" {
		path := binaryPath(requested)
		if isExecutable(path) {
			return path, nil
		}
		if err := downloadAndExtract(ctx, requested); err != nil {
			return "", err
		}
		if !isExecutable(path) {
			return "", fmt.Errorf("%w: download finished but no binary at %s", ErrBinaryNotFound, path)
		}
		return path, nil
	}

	effective := effectiveVersion()
	path := binaryPath(effective)
	if isExecutable(path) {
		return path, nil
	}
	if platform := versionForPlatform(); effective != platform {
		if fallback := binaryPath(""); isExecutable(fallback) {
			return fallback, nil
		}
	}
	if err := downloadAndExtract(ctx, ""); err != nil {
		return "", err
	}
	path = binaryPath("")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%w: download finished but no binary at %s", ErrBinaryNotFound, path)
	}
	return path, nil
}

// ---- platform / paths -------------------------------------------------------

func platformTag() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux-x64", nil
	case "linux/arm64":
		return "linux-arm64", nil
	case "darwin/arm64":
		return "darwin-arm64", nil
	case "darwin/amd64":
		return "darwin-x64", nil
	case "windows/amd64":
		return "windows-x64", nil
	default:
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}
}

func checkPlatform() error {
	tag, err := platformTag()
	if err != nil {
		return err
	}
	if _, ok := platformVersions[tag]; !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedPlatform, tag)
	}
	return nil
}

func archiveExt() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

func archiveName(tag string) string { return "cloakbrowser-" + tag + archiveExt() }

func versionForPlatform() string {
	tag, _ := platformTag()
	if v, ok := platformVersions[tag]; ok {
		return v
	}
	return chromiumVersion
}

func normalizeVersion(arg string) (string, error) {
	raw := arg
	if raw == "" {
		raw = env("ANTARES_STEALTH_VERSION", "CLOAKBROWSER_VERSION")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !versionPinRe.MatchString(raw) {
		return "", fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
	}
	return raw, nil
}

// cacheDir defaults to ~/.cloakbrowser so a binary the upstream tooling already
// downloaded is reused. Override with ANTARES_STEALTH_CACHE / CLOAKBROWSER_CACHE_DIR.
func cacheDir() string {
	if c := env("ANTARES_STEALTH_CACHE", "CLOAKBROWSER_CACHE_DIR"); c != "" {
		return c
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".cloakbrowser")
}

func binaryDir(version string) string {
	if version == "" {
		version = versionForPlatform()
	}
	return filepath.Join(cacheDir(), "chromium-"+version)
}

func binaryPath(version string) string {
	dir := binaryDir(version)
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(dir, "Chromium.app", "Contents", "MacOS", "Chromium")
	case "windows":
		return filepath.Join(dir, "chrome.exe")
	default:
		return filepath.Join(dir, "chrome")
	}
}

// effectiveVersion prefers a cache marker naming a newer, already-downloaded
// version over the hardcoded base.
func effectiveVersion() string {
	base := versionForPlatform()
	cache := cacheDir()
	var markers []string
	if tag, err := platformTag(); err == nil {
		markers = append(markers, filepath.Join(cache, "latest_version_"+tag))
	}
	markers = append(markers, filepath.Join(cache, "latest_version"))
	for _, m := range markers {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(data))
		if v == "" || !versionNewer(v, base) {
			continue
		}
		if _, err := os.Stat(binaryPath(v)); err == nil {
			return v
		}
	}
	return base
}

func versionNewer(a, b string) bool {
	pa, pb := parseVer(a), parseVer(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return len(pa) > len(pb)
}

func parseVer(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func downloadBaseURL() string {
	if u := env("ANTARES_STEALTH_DOWNLOAD_URL", "CLOAKBROWSER_DOWNLOAD_URL"); u != "" {
		return u
	}
	return defaultDownloadBaseURL
}

func hasCustomDownloadURL() bool {
	return env("ANTARES_STEALTH_DOWNLOAD_URL", "CLOAKBROWSER_DOWNLOAD_URL") != ""
}

// ---- download / verify / extract -------------------------------------------

func downloadAndExtract(ctx context.Context, version string) error {
	v := version
	if v == "" {
		v = versionForPlatform()
	}
	tag, err := platformTag()
	if err != nil {
		return err
	}
	name := archiveName(tag)
	primary := fmt.Sprintf("%s/chromium-v%s/%s", downloadBaseURL(), v, name)
	fallback := fmt.Sprintf("%s/chromium-v%s/%s", githubDownloadBaseURL, v, name)

	dir := binaryDir(version)
	path := binaryPath(version)
	if parent := filepath.Dir(dir); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("%w: creating cache dir: %v", ErrDownload, err)
		}
	}

	tmp, err := os.CreateTemp("", "antares-stealth-*"+archiveExt())
	if err != nil {
		return fmt.Errorf("%w: creating temp file: %v", ErrDownload, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if derr := downloadFile(ctx, primary, tmpPath); derr != nil {
		if hasCustomDownloadURL() {
			return derr
		}
		if ferr := downloadFile(ctx, fallback, tmpPath); ferr != nil {
			return ferr
		}
	}
	if err := verifyDownload(ctx, tmpPath, version); err != nil {
		return err
	}
	return extractArchive(tmpPath, dir, path)
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownload, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET %s: %v", ErrDownload, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: GET %s: status %d", ErrDownload, url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer f.Close()
	if _, err := io.CopyBuffer(f, resp.Body, make([]byte, chunkSize)); err != nil {
		return fmt.Errorf("%w: streaming %s: %v", ErrDownload, url, err)
	}
	return nil
}

func verifyDownload(ctx context.Context, filePath, version string) error {
	tag, err := platformTag()
	if err != nil {
		return err
	}
	name := archiveName(tag)

	if hasCustomDownloadURL() {
		// Self-hosted mirror: the signature scheme does not apply. Fall back to
		// a same-origin checksum, skippable for a trusted mirror.
		if strings.EqualFold(env("ANTARES_STEALTH_SKIP_CHECKSUM", "CLOAKBROWSER_SKIP_CHECKSUM"), "true") {
			return nil
		}
		checksums := fetchChecksums(ctx, version)
		if checksums == nil {
			return nil
		}
		expected, ok := checksums[name]
		if !ok {
			return nil
		}
		return verifyChecksum(filePath, expected)
	}

	manifest, sig, ok := fetchSignedManifest(ctx, version)
	if !ok {
		return fmt.Errorf("%w: no signed SHA256SUMS for this release — refusing an unverified binary", ErrVerification)
	}
	if err := verifySignature(manifest, sig); err != nil {
		return err
	}
	text := string(manifest)
	requested := version
	if requested == "" {
		requested = versionForPlatform()
	}
	if declared := manifestVersion(text); declared != requested {
		got := declared
		if got == "" {
			got = "none"
		}
		return fmt.Errorf("%w: signed manifest declares %s, requested %s (possible downgrade)", ErrVerification, got, requested)
	}
	checksums := parseChecksums(text)
	expected, ok := checksums[name]
	if !ok {
		return fmt.Errorf("%w: signed manifest has no entry for %s", ErrVerification, name)
	}
	return verifyChecksum(filePath, expected)
}

func fetchSignedManifest(ctx context.Context, version string) (manifest, sig []byte, ok bool) {
	v := version
	if v == "" {
		v = versionForPlatform()
	}
	bases := []string{
		fmt.Sprintf("%s/chromium-v%s", downloadBaseURL(), v),
		fmt.Sprintf("%s/chromium-v%s", githubDownloadBaseURL, v),
	}
	for _, base := range bases {
		m, err := fetchBytes(ctx, base+"/SHA256SUMS")
		if err != nil {
			continue
		}
		s, err := fetchBytes(ctx, base+"/SHA256SUMS.sig")
		if err != nil {
			continue
		}
		return m, s, true
	}
	return nil, nil, false
}

func fetchChecksums(ctx context.Context, version string) map[string]string {
	v := version
	if v == "" {
		v = versionForPlatform()
	}
	urls := []string{fmt.Sprintf("%s/chromium-v%s/SHA256SUMS", downloadBaseURL(), v)}
	if !hasCustomDownloadURL() {
		urls = append(urls, fmt.Sprintf("%s/chromium-v%s/SHA256SUMS", githubDownloadBaseURL, v))
	}
	for _, u := range urls {
		data, err := fetchBytes(ctx, u)
		if err != nil {
			continue
		}
		return parseChecksums(string(data))
	}
	return nil
}

func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// verifySignature checks a detached Ed25519 signature (base64 of the 64-byte
// raw signature) over the manifest bytes. Any pinned key validating passes; it
// fails closed.
func verifySignature(manifest, sigB64 []byte) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		return fmt.Errorf("%w: SHA256SUMS.sig is not valid base64: %v", ErrVerification, err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: SHA256SUMS.sig is %d bytes, expected %d", ErrVerification, len(sig), ed25519.SignatureSize)
	}
	for _, keyB64 := range signingPubkeys {
		raw, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(raw), manifest, sig) {
			return nil
		}
	}
	return fmt.Errorf("%w: no pinned key validated the manifest signature", ErrVerification)
}

func manifestVersion(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "version="); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func parseChecksums(text string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		i := strings.IndexAny(line, " \t\r\f\v")
		if i < 0 {
			continue
		}
		hashVal := strings.ToLower(line[:i])
		name := strings.TrimLeft(line[i+1:], " \t\r\f\v")
		if name == "" || len(hashVal) != 64 || !isHex(hashVal) {
			continue
		}
		out[strings.TrimLeft(name, "*")] = hashVal
	}
	return out
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func verifyChecksum(filePath, expected string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerification, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, chunkSize)); err != nil {
		return fmt.Errorf("%w: hashing %s: %v", ErrVerification, filePath, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expected {
		return fmt.Errorf("%w: checksum mismatch: expected %s, got %s", ErrVerification, expected, got)
	}
	return nil
}

func extractArchive(archivePath, destDir, binPath string) error {
	if _, err := os.Stat(destDir); err == nil {
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("%w: %v", ErrExtraction, err)
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		if err := extractZip(archivePath, destDir); err != nil {
			return err
		}
	} else {
		if err := extractTar(archivePath, destDir); err != nil {
			return err
		}
	}
	if err := flattenSingleSubdir(destDir); err != nil {
		return err
	}
	if _, err := os.Stat(binPath); err == nil {
		if err := makeExecutable(binPath); err != nil {
			return err
		}
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("xattr", "-cr", destDir).Run()
	}
	return nil
}

func canonicalDest(destDir string) string {
	if resolved, err := filepath.EvalSymlinks(destDir); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(destDir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(destDir)
}

func isWithin(dest, candidate string) bool {
	cleaned := filepath.Clean(candidate)
	if cleaned == dest {
		return true
	}
	rel, err := filepath.Rel(dest, cleaned)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func suspiciousLink(target string) bool {
	if target == "" || filepath.IsAbs(target) || strings.HasPrefix(target, "/") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(target), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func extractTar(archivePath, destDir string) error {
	dest := canonicalDest(destDir)
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrExtraction, err)
		}
		target := filepath.Join(dest, hdr.Name)
		if !isWithin(dest, target) {
			return fmt.Errorf("%w: %s", ErrPathTraversal, hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			if suspiciousLink(hdr.Linkname) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("%w: %v", ErrExtraction, err)
			}
			_ = os.Remove(target)
			if hdr.Typeflag == tar.TypeSymlink {
				if err := os.Symlink(hdr.Linkname, target); err != nil {
					return fmt.Errorf("%w: %v", ErrExtraction, err)
				}
			} else {
				linkDest := filepath.Join(dest, hdr.Linkname)
				if !isWithin(dest, linkDest) {
					continue
				}
				if err := os.Link(linkDest, target); err != nil {
					return fmt.Errorf("%w: %v", ErrExtraction, err)
				}
			}
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return fmt.Errorf("%w: %v", ErrExtraction, err)
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	dest := canonicalDest(destDir)
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	defer zr.Close()
	for _, entry := range zr.File {
		if !isWithin(dest, filepath.Join(dest, entry.Name)) {
			return fmt.Errorf("%w: %s", ErrPathTraversal, entry.Name)
		}
	}
	for _, entry := range zr.File {
		target := filepath.Join(dest, entry.Name)
		mode := entry.Mode()
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, mode.Perm()|0o700); err != nil {
				return fmt.Errorf("%w: %v", ErrExtraction, err)
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			if err := extractZipSymlink(entry, target); err != nil {
				return err
			}
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrExtraction, err)
		}
		werr := writeFile(target, rc, mode.Perm())
		rc.Close()
		if werr != nil {
			return werr
		}
	}
	return nil
}

func extractZipSymlink(entry *zip.File, target string) error {
	rc, err := entry.Open()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	if suspiciousLink(string(data)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	_ = os.Remove(target)
	if err := os.Symlink(string(data), target); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	defer out.Close()
	if _, err := io.CopyBuffer(out, r, make([]byte, chunkSize)); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	return nil
}

// flattenSingleSubdir lifts the contents of a lone non-.app subdirectory up one
// level, so the binary lands at the expected path regardless of how the archive
// nested it.
func flattenSingleSubdir(destDir string) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	if len(entries) != 1 {
		return nil
	}
	sub := entries[0]
	if !sub.IsDir() || strings.HasSuffix(sub.Name(), ".app") {
		return nil
	}
	subdir := filepath.Join(destDir, sub.Name())
	items, err := os.ReadDir(subdir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	for _, item := range items {
		if err := os.Rename(filepath.Join(subdir, item.Name()), filepath.Join(destDir, item.Name())); err != nil {
			return fmt.Errorf("%w: %v", ErrExtraction, err)
		}
	}
	if err := os.Remove(subdir); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	return nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func makeExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	if err := os.Chmod(path, info.Mode()|0o111); err != nil {
		return fmt.Errorf("%w: %v", ErrExtraction, err)
	}
	return nil
}
