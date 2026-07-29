package intercept

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Chromium-family browser interception, ported from HTTP Toolkit's
// chromium-based-interceptors. The whole trick is launch-time flags: point the
// browser at the proxy with --proxy-server, and make it trust every leaf the
// proxy signs with --ignore-certificate-errors-spki-list=<SPKI fp> — no
// trust-store install at all. The "fresh" variant uses a throwaway profile.

// browserVariant maps an id to its per-OS executable candidates.
type browserVariant struct {
	id    string
	label string
	// candidates per GOOS; absolute paths are stat'd, bare names are LookPath'd.
	darwin  []string
	linux   []string
	windows []string
	// brave needs its component updater left on (adblock lists), so we skip the
	// update-disabling flags for it.
	keepUpdater bool
}

var browserVariants = []browserVariant{
	{
		id: "chrome", label: "Chrome",
		darwin:  []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		linux:   []string{"google-chrome", "google-chrome-stable"},
		windows: []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`},
	},
	{
		id: "chromium", label: "Chromium",
		darwin:  []string{"/Applications/Chromium.app/Contents/MacOS/Chromium"},
		linux:   []string{"chromium", "chromium-browser"},
		windows: []string{`C:\Program Files\Chromium\Application\chrome.exe`},
	},
	{
		id: "edge", label: "Edge",
		darwin:  []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		linux:   []string{"microsoft-edge", "microsoft-edge-stable"},
		windows: []string{`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`},
	},
	{
		id: "brave", label: "Brave", keepUpdater: true,
		darwin:  []string{"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		linux:   []string{"brave-browser", "brave"},
		windows: []string{`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`},
	},
	{
		id: "opera", label: "Opera",
		darwin:  []string{"/Applications/Opera.app/Contents/MacOS/Opera"},
		linux:   []string{"opera"},
		windows: []string{`C:\Program Files\Opera\launcher.exe`},
	},
}

func (v browserVariant) candidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return v.darwin
	case "windows":
		return v.windows
	default:
		return v.linux
	}
}

// exe resolves the browser's path, or "" if not installed.
func (v browserVariant) exe() string {
	for _, c := range v.candidates() {
		if strings.ContainsAny(c, `/\`) {
			if _, err := os.Stat(c); err == nil {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// browserInterceptor is one Chromium-family "fresh browser" interceptor.
type browserInterceptor struct{ v browserVariant }

// Browsers returns an interceptor per known Chromium-family variant.
func Browsers() []Interceptor {
	out := make([]Interceptor, 0, len(browserVariants))
	for _, v := range browserVariants {
		out = append(out, &browserInterceptor{v: v})
	}
	return out
}

func (b *browserInterceptor) ID() string       { return "fresh-" + b.v.id }
func (b *browserInterceptor) Label() string     { return b.v.label }
func (b *browserInterceptor) Category() string  { return "browser" }

func (b *browserInterceptor) Available(_ context.Context) (bool, string) {
	if b.v.exe() == "" {
		return false, fmt.Sprintf("%s is not installed", b.v.label)
	}
	return true, ""
}

func (b *browserInterceptor) Activate(_ context.Context, opts ActivateOpts) (Session, error) {
	exe := b.v.exe()
	if exe == "" {
		return nil, fmt.Errorf("%s is not installed", b.v.label)
	}
	profileDir, err := os.MkdirTemp("", "antares-"+b.v.id+"-*")
	if err != nil {
		return nil, err
	}

	url, _ := opts.Extra["url"].(string)
	args := []string{
		"--proxy-server=http://" + opts.ProxyAddr,
		// Force even loopback through the proxy (Chromium skips it by default).
		"--proxy-bypass-list=<-loopback>",
		// Trust every leaf the proxy signs — no cert install needed.
		"--ignore-certificate-errors-spki-list=" + opts.SPKIFingerprint,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if !b.v.keepUpdater {
		args = append(args,
			"--disable-background-networking",
			"--check-for-update-interval=31536000",
		)
	}
	if strings.TrimSpace(url) != "" {
		args = append(args, url)
	}

	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}

	s := &browserSession{
		id:      b.ID() + "-" + fmt.Sprint(cmd.Process.Pid),
		variant: b.v.id,
		cmd:     cmd,
		profile: profileDir,
	}
	// Clean up the throwaway profile when the browser exits.
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		s.done = true
		s.mu.Unlock()
		_ = os.RemoveAll(profileDir)
	}()
	return s, nil
}

type browserSession struct {
	id      string
	variant string
	cmd     *exec.Cmd
	profile string
	mu      sync.Mutex
	done    bool
}

func (s *browserSession) ID() string          { return s.id }
func (s *browserSession) Interceptor() string { return "fresh-" + s.variant }
func (s *browserSession) Info() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"variant": s.variant, "pid": s.cmd.Process.Pid, "running": !s.done}
}
func (s *browserSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Kill()
}
