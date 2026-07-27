package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testPage is deliberately ordinary: a heading, a form, and a script that
// rewrites the DOM after a click, which is what breaks naive scrapers.
const testPage = `<!doctype html>
<html><head><title>Antares Test</title></head>
<body>
  <h1>Search the archive</h1>
  <form onsubmit="event.preventDefault(); show()">
    <label for="q">Query</label>
    <input id="q" name="q" placeholder="what are you looking for">
    <select id="scope"><option value="all">Everything</option><option value="docs">Docs only</option></select>
    <button type="submit">Search</button>
  </form>
  <a href="/about">About this archive</a>
  <div id="out"></div>
  <script>
    function show() {
      document.getElementById('out').textContent =
        'Results for ' + document.getElementById('q').value +
        ' in ' + document.getElementById('scope').value;
    }
  </script>
</body></html>`

func newSession(t *testing.T) (*Session, string) {
	t.Helper()
	if _, err := FindExecutable(""); err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/about" {
			_, _ = w.Write([]byte(`<!doctype html><title>About</title><h1>About this archive</h1>`))
			return
		}
		_, _ = w.Write([]byte(testPage))
	}))
	t.Cleanup(srv.Close)

	s := New(Options{Headless: true, Width: 1024, Height: 768})
	t.Cleanup(s.Stop)
	return s, srv.URL
}

func TestNavigateAndSnapshot(t *testing.T) {
	s, url := newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := s.Navigate(ctx, url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	title, err := s.Title(ctx)
	if err != nil || title != "Antares Test" {
		t.Fatalf("title = %q, %v", title, err)
	}

	snap, err := s.Snapshot(ctx, 50)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, want := range []string{"heading", "Search the archive", "textbox", "button", "link"} {
		if !strings.Contains(snap, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, snap)
		}
	}
	// References must be usable, which means numbered from e1.
	if !strings.Contains(snap, "e1 ") {
		t.Fatalf("snapshot has no e1 reference:\n%s", snap)
	}
}

func TestTypeSelectAndClick(t *testing.T) {
	s, url := newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := s.Navigate(ctx, url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	snap, err := s.Snapshot(ctx, 50)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	textbox := refWithRole(t, snap, "textbox")
	if _, err := s.Type(ctx, textbox, "starlight", false); err != nil {
		t.Fatalf("type: %v", err)
	}
	selectRef := refWithRole(t, snap, "select")
	if _, err := s.Select(ctx, selectRef, "Docs only"); err != nil {
		t.Fatalf("select: %v", err)
	}
	button := refWithRole(t, snap, "button")
	if _, err := s.Click(ctx, button); err != nil {
		t.Fatalf("click: %v", err)
	}

	body, err := s.Text(ctx, "", 2000)
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if !strings.Contains(body, "Results for starlight in docs") {
		t.Fatalf("the click did not run the page's handler; body:\n%s", body)
	}
}

func TestStaleReferenceIsReported(t *testing.T) {
	s, url := newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := s.Navigate(ctx, url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if _, err := s.Snapshot(ctx, 50); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// A reference far past the end of the list is the same situation as one
	// that has gone stale, and must say so rather than silently doing nothing.
	if _, err := s.Click(ctx, "e999"); err == nil {
		t.Fatal("expected an error for a reference that is not on the page")
	}
	if _, err := s.Click(ctx, "not-a-ref"); err == nil {
		t.Fatal("expected an error for a malformed reference")
	}
}

func TestWaitForAndBack(t *testing.T) {
	s, url := newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := s.Navigate(ctx, url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if err := s.Navigate(ctx, url+"/about"); err != nil {
		t.Fatalf("navigate about: %v", err)
	}
	if _, err := s.WaitFor(ctx, "About this archive", 5*time.Second); err != nil {
		t.Fatalf("wait_for: %v", err)
	}
	if err := s.Back(ctx); err != nil {
		t.Fatalf("back: %v", err)
	}
	title, _ := s.Title(ctx)
	if title != "Antares Test" {
		t.Fatalf("after going back the title is %q", title)
	}

	// A phrase that is not there must time out rather than claim success.
	short, cancelShort := context.WithTimeout(ctx, 5*time.Second)
	defer cancelShort()
	if _, err := s.WaitFor(short, "definitely not present", 1*time.Second); err == nil {
		t.Fatal("expected wait_for to fail for absent text")
	}
}

func TestScreenshot(t *testing.T) {
	s, url := newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := s.Navigate(ctx, url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	png, err := s.Screenshot(ctx, false)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(png) < 1000 || string(png[1:4]) != "PNG" {
		t.Fatalf("got %d bytes that do not look like a PNG", len(png))
	}
}

// refWithRole pulls the first reference of a given role out of a snapshot.
func refWithRole(t *testing.T, snapshot, role string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == role {
			return fields[0]
		}
	}
	t.Fatalf("no %s in snapshot:\n%s", role, snapshot)
	return ""
}
