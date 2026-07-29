package intercept

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Trust helpers: identifiers a client needs to install/verify the CA, plus the
// per-OS commands that add it to a trust store. Everything here is pure stdlib;
// the actual install is left to the operator (we print the exact command),
// which keeps antares a single static binary with no privileged side effects.

// SubjectHashOld returns the OpenSSL "subject_hash_old" (8 hex chars) of the CA
// subject — the filename Android and some Linux/NSS flows expect for a trusted
// root (e.g. "<hash>.0"). It is the first 4 bytes of MD5(RawSubject), read
// little-endian. RawSubject is used directly to match OpenSSL's canonical form.
func SubjectHashOld(c *x509.Certificate) string {
	sum := md5.Sum(c.RawSubject) //nolint:gosec // OpenSSL-compatible identifier, not security
	return fmt.Sprintf("%08x", binary.LittleEndian.Uint32(sum[:4]))
}

// Fingerprint returns the hex SHA-1 of the whole CA cert — the value trust
// stores and dialogs show so an operator can confirm they trusted the right one.
func Fingerprint(c *x509.Certificate) string {
	sum := sha1.Sum(c.Raw) //nolint:gosec // fingerprint identifier, not security
	return fmt.Sprintf("%x", sum[:])
}

// InstallTarget is one way to trust the CA on this machine: a human label, the
// exact shell command, and whether the tool it needs is actually present.
type InstallTarget struct {
	Label     string `json:"label"`
	Command   string `json:"command"`
	Tool      string `json:"tool"`
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
}

// InstallLocations returns the trust-install options for the current OS, each
// flagged with whether its tool is on PATH so the UI can show a ready command
// or an "install this first" hint. certPath is the on-disk CA PEM.
func InstallLocations(certPath string, subjectHash string) []InstallTarget {
	have := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }
	var out []InstallTarget

	switch runtime.GOOS {
	case "darwin":
		out = append(out, InstallTarget{
			Label:     "macOS system keychain (admin)",
			Command:   fmt.Sprintf("sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %q", certPath),
			Tool:      "security",
			Available: have("security"),
		})
	case "linux":
		out = append(out, InstallTarget{
			Label:     "Linux system trust (Debian/Ubuntu)",
			Command:   fmt.Sprintf("sudo cp %q /usr/local/share/ca-certificates/antares-intercept.crt && sudo update-ca-certificates", certPath),
			Tool:      "update-ca-certificates",
			Available: have("update-ca-certificates"),
		})
		out = append(out, InstallTarget{
			Label:     "Linux system trust (Fedora/RHEL)",
			Command:   fmt.Sprintf("sudo cp %q /etc/pki/ca-trust/source/anchors/antares-intercept.crt && sudo update-ca-trust", certPath),
			Tool:      "update-ca-trust",
			Available: have("update-ca-trust"),
		})
	case "windows":
		out = append(out, InstallTarget{
			Label:     "Windows Root store (admin)",
			Command:   fmt.Sprintf("certutil -addstore -f Root %q", certPath),
			Tool:      "certutil",
			Available: have("certutil"),
		})
	}

	// Firefox/NSS is cross-platform and ignores the OS store — always offered.
	out = append(out, InstallTarget{
		Label:     "Firefox / NSS profile",
		Command:   fmt.Sprintf("certutil -A -n \"antares Intercept\" -t \"C,,\" -i %q -d sql:$HOME/.mozilla/firefox/<profile>", certPath),
		Tool:      "certutil",
		Available: have("certutil"),
		Note:      "certutil here is the NSS tool (libnss3-tools / nss), not Windows certutil.",
	})

	// Android (device over adb): push as <subjectHashOld>.0 into the system
	// store — needs root. Shown when adb is present.
	if strings.TrimSpace(subjectHash) != "" {
		out = append(out, InstallTarget{
			Label: "Android device (rooted, via adb)",
			Command: fmt.Sprintf(
				"adb push %q /sdcard/%s.0 && adb shell su -c 'mount -o rw,remount /system && cp /sdcard/%s.0 /system/etc/security/cacerts/ && chmod 644 /system/etc/security/cacerts/%s.0'",
				certPath, subjectHash, subjectHash, subjectHash),
			Tool:      "adb",
			Available: have("adb"),
			Note:      "Requires a rooted device; unrooted devices can only trust user certs.",
		})
	}

	return out
}
