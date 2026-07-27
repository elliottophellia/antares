package httpshim

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sardanioss/httpcloak"
)

func TestInstallWritesExecutableShims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shims are not installed on windows")
	}
	t.Setenv("ANTARES_HOME", t.TempDir())
	dir, err := Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, tool := range Tools {
		p := filepath.Join(dir, tool)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s shim missing: %v", tool, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s shim is not executable: %v", tool, info.Mode())
		}
		body, _ := os.ReadFile(p)
		if want := "_httpshim " + tool; !contains(string(body), want) {
			t.Fatalf("%s shim does not re-invoke antares: %s", tool, body)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shims are not installed on windows")
	}
	t.Setenv("ANTARES_HOME", t.TempDir())
	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
	curl := filepath.Join(config_home(t), "shims", "curl")
	before, err := os.Stat(curl)
	if err != nil {
		t.Fatal(err)
	}
	// A second install must not rewrite an unchanged file.
	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(curl)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("second Install rewrote the unchanged shim")
	}
}

func TestIsHTTPURL(t *testing.T) {
	for _, ok := range []string{"http://x", "https://x/y"} {
		if !isHTTPURL(ok) {
			t.Fatalf("%q should be an http url", ok)
		}
	}
	for _, no := range []string{"ftp://x", "x", "file:///y", "-H"} {
		if isHTTPURL(no) {
			t.Fatalf("%q should not be an http url", no)
		}
	}
}

func TestBasename(t *testing.T) {
	cases := map[string]string{
		"https://a.com/path/file.json": "file.json",
		"https://a.com/":               "index.html",
		"https://a.com":                "index.html",
		"https://a.com/x/?q=1":         "x",
	}
	for in, want := range cases {
		if got := basename(in, "index.html"); got != want {
			t.Errorf("basename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSeconds(t *testing.T) {
	if parseSeconds("5") != 5*time.Second {
		t.Fatal("5 should be five seconds")
	}
	if parseSeconds("0.5") != 500*time.Millisecond {
		t.Fatal("0.5 should be half a second")
	}
	if parseSeconds("nope") != 0 {
		t.Fatal("garbage should be zero")
	}
}

func TestStatusLineNormalisesProtocol(t *testing.T) {
	cases := map[string]string{
		"h2":       "HTTP/2 200",
		"http/2":   "HTTP/2 200",
		"h3":       "HTTP/3 200",
		"":         "HTTP/1.1 200",
		"http/1.1": "HTTP/1.1 200",
	}
	for proto, want := range cases {
		got := statusLine(&httpcloak.Response{StatusCode: 200, Protocol: proto})
		if got != want {
			t.Errorf("statusLine(%q) = %q, want %q", proto, got, want)
		}
	}
}

func TestSameDir(t *testing.T) {
	if sameDir("/a/b", "") {
		t.Fatal("empty target is never the same dir")
	}
	if !sameDir("/a/b", "/a/b") {
		t.Fatal("identical paths are the same dir")
	}
	if !sameDir("/a/b/", "/a/b") {
		t.Fatal("trailing slash should not matter")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func config_home(t *testing.T) string {
	t.Helper()
	return os.Getenv("ANTARES_HOME")
}
