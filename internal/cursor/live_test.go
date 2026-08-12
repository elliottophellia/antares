package cursor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveCursorMetadata(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
	if key == "" {
		t.Skip("set CURSOR_API_KEY to run Cursor metadata smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := New(Options{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	me, err := client.Me(ctx)
	if err != nil {
		t.Fatalf("Cursor /v1/me: %v", err)
	}
	models, err := client.Models(ctx)
	if err != nil {
		t.Fatalf("Cursor /v1/models: %v", err)
	}
	if me.APIKeyName == "" || len(models.Items) == 0 {
		t.Fatalf("incomplete metadata: me=%+v models=%d", me, len(models.Items))
	}
	t.Logf("Cursor key %q exposes %d models", me.APIKeyName, len(models.Items))
}
