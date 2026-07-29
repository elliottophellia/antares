package intercept

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Android interception over adb. Two modes, chosen automatically:
//   - app-free: `adb shell settings put global http_proxy <host>:<port>` routes
//     the device's traffic through the proxy. Only apps that honour the system
//     proxy AND trust a user/http CA are captured (most plain HTTP; HTTPS only
//     where the app trusts our CA).
//   - root: additionally push the CA into the system trust store as
//     <subjectHashOld>.0, which makes EVERY app trust the proxy's leaves.
//
// Everything shells out to `adb`; there is no bundled APK. Needs adb on PATH and
// a single connected device (USB or `adb connect`). The proxy must be reachable
// from the device — for a physical device that means the host's LAN IP, not
// 127.0.0.1; we surface a warning rather than guess the address.

type androidInterceptor struct{}

func (androidInterceptor) ID() string       { return "android" }
func (androidInterceptor) Label() string    { return "Android device" }
func (androidInterceptor) Category() string { return "mobile" }

func (androidInterceptor) Available(ctx context.Context) (bool, string) {
	if _, err := exec.LookPath("adb"); err != nil {
		return false, "Android interception needs 'adb' (Android platform-tools: brew install android-platform-tools / apt install adb)."
	}
	devs, err := adbDevices(ctx)
	if err != nil {
		return false, "adb is installed but failed to run: " + err.Error()
	}
	switch len(devs) {
	case 0:
		return false, "No Android device is connected. Plug one in (USB debugging on) or `adb connect <ip>`."
	case 1:
		return true, ""
	default:
		return false, fmt.Sprintf("%d devices connected — disconnect all but one (multi-device targeting is not supported yet).", len(devs))
	}
}

func (androidInterceptor) Activate(ctx context.Context, opts ActivateOpts) (Session, error) {
	devs, err := adbDevices(ctx)
	if err != nil {
		return nil, err
	}
	if len(devs) != 1 {
		return nil, fmt.Errorf("need exactly one connected device, found %d", len(devs))
	}
	dev := devs[0]

	// The device reaches the proxy by network; a loopback addr only works for an
	// emulator with a host mapping. Rewrite 127.0.0.1 to 10.0.2.2 (the emulator's
	// host alias); for a physical device the operator must use the host LAN IP.
	host, port := splitHostPort(opts.ProxyAddr)
	warn := ""
	if host == "127.0.0.1" || host == "localhost" {
		if isEmulator(dev) {
			host = "10.0.2.2" // emulator alias for the host loopback
		} else {
			warn = "device may not reach 127.0.0.1 — set the proxy host to this machine's LAN IP if capture is empty."
		}
	}
	proxyHost := host + ":" + port

	// App-free: set the global HTTP proxy.
	if out, err := adb(ctx, dev, "shell", "settings", "put", "global", "http_proxy", proxyHost); err != nil {
		return nil, fmt.Errorf("set proxy: %v (%s)", err, out)
	}

	// Root (best effort): push the CA into the system store so all apps trust it.
	rooted := false
	rootNote := ""
	if opts.CACertPath != "" {
		if hash := caSubjectHash(opts); hash != "" && adbRootAvailable(ctx, dev) {
			if err := pushSystemCert(ctx, dev, opts.CACertPath, hash); err == nil {
				rooted = true
			} else {
				rootNote = "root cert push failed: " + err.Error()
			}
		}
	}
	if !rooted && rootNote == "" {
		rootNote = "device not rooted — only apps trusting a user cert / plain HTTP are intercepted."
	}

	return &androidSession{dev: dev, proxy: proxyHost, rooted: rooted, note: strings.TrimSpace(warn + " " + rootNote)}, nil
}

// ---- session ----------------------------------------------------------------

type androidSession struct {
	dev    string
	proxy  string
	rooted bool
	note   string
}

func (s *androidSession) ID() string          { return "android-" + s.dev }
func (s *androidSession) Interceptor() string { return "android" }
func (s *androidSession) Info() map[string]any {
	return map[string]any{"device": s.dev, "proxy": s.proxy, "rooted": s.rooted, "note": s.note}
}
func (s *androidSession) Stop() error {
	// Clear the global proxy so the device goes back to normal.
	_, err := adb(context.Background(), s.dev, "shell", "settings", "put", "global", "http_proxy", ":0")
	return err
}

// ---- adb helpers ------------------------------------------------------------

func adb(ctx context.Context, serial string, args ...string) (string, error) {
	full := append([]string{"-s", serial}, args...)
	cmd := exec.CommandContext(ctx, "adb", full...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// adbDevices lists connected device serials (state "device" only).
func adbDevices(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "adb", "devices")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var devs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "device" {
			devs = append(devs, f[0])
		}
	}
	return devs, nil
}

func isEmulator(serial string) bool { return strings.HasPrefix(serial, "emulator-") }

// adbRootAvailable reports whether we can get a root shell on the device.
func adbRootAvailable(ctx context.Context, dev string) bool {
	// `adb root` restarts adbd as root on userdebug/eng builds; harmless on
	// production (it just says "cannot run as root").
	_, _ = adb(ctx, dev, "root")
	out, _ := adb(ctx, dev, "shell", "id")
	if strings.Contains(out, "uid=0") {
		return true
	}
	// Fall back to `su -c id` for Magisk-style root.
	out, _ = adb(ctx, dev, "shell", "su", "-c", "id")
	return strings.Contains(out, "uid=0")
}

// pushSystemCert installs the CA as <hash>.0 in the system trust store. Requires
// root; tries a plain remount first, then su.
func pushSystemCert(ctx context.Context, dev, certPath, hash string) error {
	tmp := "/sdcard/" + hash + ".0"
	if out, err := adb(ctx, dev, "push", certPath, tmp); err != nil {
		return fmt.Errorf("push: %v (%s)", err, out)
	}
	target := "/system/etc/security/cacerts/" + hash + ".0"
	script := fmt.Sprintf(
		"mount -o rw,remount /system 2>/dev/null; cp %s %s && chmod 644 %s && echo OK",
		tmp, target, target)
	// Try direct (adb root), then su.
	if out, _ := adb(ctx, dev, "shell", script); strings.Contains(out, "OK") {
		return nil
	}
	if out, _ := adb(ctx, dev, "shell", "su", "-c", script); strings.Contains(out, "OK") {
		return nil
	}
	return fmt.Errorf("could not write system cert (device may use a read-only APEX cacerts store)")
}

// caSubjectHash pulls the CA subject hash out of the activate opts. The opts
// carry the SPKI and cert path; we compute the OpenSSL subject hash from the
// on-disk cert via the shared trust helper is not available here without the
// cert, so the tool layer passes it through Extra when known.
func caSubjectHash(opts ActivateOpts) string {
	if h, ok := opts.Extra["subject_hash"].(string); ok {
		return h
	}
	return ""
}

func splitHostPort(addr string) (string, string) {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i], addr[i+1:]
	}
	return addr, "8899"
}
