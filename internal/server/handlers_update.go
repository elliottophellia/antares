package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/version"
)

// releasesAPI is the GitHub endpoint for the newest published release.
const releasesAPI = "https://api.github.com/repos/enowdev/antares/releases/latest"

// updateCache memoises the latest-release lookup so opening the dashboard (and
// the startup check) don't hammer GitHub. Refreshed past updateTTL.
type updateInfo struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	Available bool      `json:"available"`
	Notes     string    `json:"notes"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
	CheckedAt time.Time `json:"checked_at"`
	Err       string    `json:"error,omitempty"`
}

var (
	updateMu   sync.Mutex
	updateData *updateInfo
	updateTTL  = 3 * time.Hour
)

// handleUpdateCheck reports whether a newer release exists. It serves a cached
// result unless it is stale or ?force=1 is passed.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	info := s.updateStatus(r.Context(), force)
	writeJSON(w, http.StatusOK, info)
}

// updateStatus returns the cached release info, refreshing it when stale.
func (s *Server) updateStatus(ctx context.Context, force bool) updateInfo {
	updateMu.Lock()
	cached := updateData
	updateMu.Unlock()
	if !force && cached != nil && time.Since(cached.CheckedAt) < updateTTL {
		return *cached
	}

	info := updateInfo{Current: version.Version, CheckedAt: time.Now()}
	latest, notes, url, published, err := fetchLatestRelease(ctx)
	if err != nil {
		info.Err = err.Error()
		// Keep the previous good data if we have it; only stamp the error.
		if cached != nil {
			cached.Err = err.Error()
			cached.CheckedAt = time.Now()
			updateMu.Lock()
			updateData = cached
			updateMu.Unlock()
			return *cached
		}
	} else {
		info.Latest = latest
		info.Notes = notes
		info.URL = url
		info.Published = published
		info.Available = isNewer(version.Version, latest)
	}

	updateMu.Lock()
	updateData = &info
	updateMu.Unlock()
	return info
}

// fetchLatestRelease pulls the newest release's tag, notes, url, and date.
func fetchLatestRelease(ctx context.Context) (tag, notes, url string, published time.Time, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		return "", "", "", time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", time.Time{}, &httpStatusError{resp.StatusCode}
	}
	var body struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", "", time.Time{}, err
	}
	return strings.TrimPrefix(body.TagName, "v"), body.Body, body.HTMLURL, body.PublishedAt, nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return "github returned status " + itoa(e.code) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// isNewer reports whether latest is a higher semantic version than current.
// A dev build (non-numeric current) is treated as always-updatable when a real
// release exists, so developers still see the banner.
func isNewer(current, latest string) bool {
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	if latest == "" {
		return false
	}
	cur := parseSemver(current)
	lat := parseSemver(latest)
	if cur == nil {
		// Non-semver current (e.g. "0.1.0-dev"): offer the update if a clean
		// release tag differs from it.
		return latest != current && current != latest
	}
	for i := 0; i < 3; i++ {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parseSemver splits "1.2.3" into [1,2,3]; returns nil if not three integers
// (a pre-release suffix like "-dev" makes it nil).
func parseSemver(v string) []int {
	base, _, _ := strings.Cut(v, "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// handleUpdateRun attempts the in-place upgrade by running the install script,
// streaming its output as SSE. On any failure it tells the client to run the
// installer command manually (the client shows that command).
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, &httpStatusError{500})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Emit JSON objects on `data:` lines, matching the dashboard's StreamEvent
	// convention ({type, message}), so the shared SSE parser handles them.
	send := func(typ, message string) {
		b, _ := json.Marshal(map[string]string{"type": typ, "message": message})
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		flusher.Flush()
	}

	script := findInstallScript()
	if script == "" {
		send("manual", updateCommand())
		send("done", "")
		return
	}

	send("log", "Running installer: "+script)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = append(os.Environ(), "ANTARES_NO_MODIFY_PATH=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		send("manual", updateCommand())
		send("done", "")
		return
	}
	cmd.Stderr = cmd.Stdout // fold stderr into the same stream
	if err := cmd.Start(); err != nil {
		send("error", err.Error())
		send("manual", updateCommand())
		send("done", "")
		return
	}
	buf := make([]byte, 4096)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			send("log", string(buf[:n]))
		}
		if rerr != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		send("error", "installer exited with an error")
		send("manual", updateCommand())
		send("done", "")
		return
	}
	send("ok", "Update installed. Restart Antares to run the new version.")
	send("done", "")
}

// findInstallScript returns a runnable path to the bundled install.sh, or "".
func findInstallScript() string {
	for _, p := range []string{"scripts/install.sh", "./scripts/install.sh"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// updateCommand is the one-liner a user can run to upgrade by hand.
func updateCommand() string {
	return "curl -fsSL https://raw.githubusercontent.com/enowdev/antares/main/scripts/install.sh | bash"
}
