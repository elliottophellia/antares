package stealth

import (
	"runtime"
	"strings"
	"testing"
)

func TestArgsCarryFingerprint(t *testing.T) {
	args := Options{}.Args()
	var hasSeed, hasPlatform, hasSandbox bool
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--fingerprint="):
			hasSeed = true
		case strings.HasPrefix(a, "--fingerprint-platform="):
			hasPlatform = true
		case a == "--no-sandbox":
			hasSandbox = true
		}
	}
	if !hasSeed || !hasPlatform || !hasSandbox {
		t.Fatalf("stealth args missing a core flag: %v", args)
	}
}

func TestArgsSpoofFields(t *testing.T) {
	args := Options{Timezone: "America/New_York", Locale: "en-US"}.Args()
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--fingerprint-timezone=America/New_York",
		"--fingerprint-locale=en-US",
		"--lang=en-US",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in %v", want, args)
		}
	}
}

func TestArgsOmitEmptySpoofs(t *testing.T) {
	joined := strings.Join(Options{}.Args(), " ")
	if strings.Contains(joined, "--fingerprint-timezone") || strings.Contains(joined, "--fingerprint-locale") {
		t.Fatalf("empty timezone/locale should not appear: %s", joined)
	}
}

func TestProxyServerArg(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"http://proxy.example:3128":    "--proxy-server=http://proxy.example:3128",
		"socks5://10.0.0.1:1080":       "--proxy-server=socks5://10.0.0.1:1080",
		"user:pass@proxy.example:3128": "", // no scheme, url.Parse yields empty host
		"http://user:pass@host:8080":   "--proxy-server=http://host:8080",
	}
	for in, want := range cases {
		if got := proxyServerArg(in); got != want {
			t.Errorf("proxyServerArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlatformTagMatchesRuntime(t *testing.T) {
	tag, err := platformTag()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if err != nil || tag != "linux-x64" {
			t.Fatalf("linux/amd64 should map to linux-x64, got %q err=%v", tag, err)
		}
	}
	if err == nil {
		if _, ok := platformVersions[tag]; !ok {
			t.Fatalf("resolved tag %q has no pinned version", tag)
		}
	}
}

func TestVersionNewer(t *testing.T) {
	if !versionNewer("146.0.7680.177.5", "146.0.7680.177.4") {
		t.Fatal(".5 should be newer than .4")
	}
	if versionNewer("146.0.7680.177.5", "146.0.7680.177.5") {
		t.Fatal("equal versions are not newer")
	}
	if versionNewer("bad", "146.0.7680.177.5") {
		t.Fatal("unparseable version is never newer")
	}
}

func TestNormalizeVersion(t *testing.T) {
	if v, err := normalizeVersion(""); err != nil || v != "" {
		t.Fatalf("empty is allowed: %q %v", v, err)
	}
	if v, err := normalizeVersion("146.0.7680.177.5"); err != nil || v != "146.0.7680.177.5" {
		t.Fatalf("valid pin rejected: %q %v", v, err)
	}
	if _, err := normalizeVersion("not-a-version"); err == nil {
		t.Fatal("garbage version pin should error")
	}
}

func TestParseChecksums(t *testing.T) {
	manifest := "version=146.0.7680.177.5\n" +
		strings.Repeat("a", 64) + "  cloakbrowser-linux-x64.tar.gz\n" +
		"tooshort  ignored.txt\n"
	sums := parseChecksums(manifest)
	if sums["cloakbrowser-linux-x64.tar.gz"] != strings.Repeat("a", 64) {
		t.Fatalf("did not parse the valid entry: %v", sums)
	}
	if _, ok := sums["ignored.txt"]; ok {
		t.Fatal("a non-64-hex line should be dropped")
	}
	if got := manifestVersion(manifest); got != "146.0.7680.177.5" {
		t.Fatalf("manifest version = %q", got)
	}
}
