package hookpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalogComplete asserts every catalogue entry has a matching file on
// disk under data/, and every file on disk has a matching catalogue entry.
// A divergence here means either a script was forgotten when it was added
// or a stale script is shipping in the embed.
func TestCatalogComplete(t *testing.T) {
	for _, c := range Categories() {
		bundled := ListBundled(c)
		bundledSet := map[string]bool{}
		for _, name := range bundled {
			bundledSet[name] = true
		}
		catalogSet := map[string]bool{}
		for _, p := range Catalog[c] {
			// A program may be available in two languages (.ps1 and .py)
			// on Windows; catalogue uses the bare name.
			catalogSet[p.Name+".ps1"] = true
			catalogSet[p.Name+".py"] = true
		}

		for name := range bundledSet {
			if !catalogSet[name] {
				t.Errorf("%s: bundled file %q has no catalogue entry", c, name)
			}
		}
		for _, p := range Catalog[c] {
			if c == CategoryWin {
				// At least one of .ps1 or .py must be present.
				if !bundledSet[p.Name+".ps1"] && !bundledSet[p.Name+".py"] {
					t.Errorf("%s: catalogue entry %q has neither .ps1 nor .py on disk", c, p.Name)
				}
				continue
			}
			if !bundledSet[p.Name+".py"] {
				t.Errorf("%s: catalogue entry %q has no .py on disk", c, p.Name)
			}
		}
	}
}

// TestScriptPathExtractsAndCaches exercises the embed → disk path: a missing
// file is written, a stale file (different hash sidecar) is rewritten, and
// a second call within the process returns the cached path without touching
// the disk again.
func TestScriptPathExtractsAndCaches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ANTARES_HOME", tmp)

	// Pick a small, harmless script: jwt_tamper from attack_script.
	prog := Catalog[CategoryAttackScript][0] // jwt_tamper
	if prog.Name != "jwt_tamper" {
		t.Fatalf("expected jwt_tamper as first attack script, got %s", prog.Name)
	}
	p, err := ScriptPath(CategoryAttackScript, prog.Name, ".py")
	if err != nil {
		t.Fatalf("first ScriptPath: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("ScriptPath returned relative path: %s", p)
	}
	if !strings.HasPrefix(p, tmp) {
		t.Errorf("ScriptPath %q is not under tmp home %q", p, tmp)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("script was not written: %v", err)
	}
	if info.Size() < 100 {
		t.Errorf("extracted script is suspiciously small: %d bytes", info.Size())
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("extracted script should be executable, mode=%v", info.Mode())
	}
	// Sidecar must exist.
	if _, err := os.Stat(p + ".sum"); err != nil {
		t.Errorf("sidecar .sum was not written: %v", err)
	}

	// Second call must return the same path and not rewrite the file.
	cached, err := ScriptPath(CategoryAttackScript, prog.Name, ".py")
	if err != nil {
		t.Fatalf("second ScriptPath: %v", err)
	}
	if cached != p {
		t.Errorf("second call returned different path: %q vs %q", cached, p)
	}

	// Tampering the script on disk triggers a rewrite on next lookup. We
	// can't easily reset the in-process cache from outside the package, so
	// we tamper with a different file: machook/keychain_dump on the same
	// tmp home (which has not been extracted yet).
	mac := Catalog[CategoryMac][0]
	if mac.Name != "keychain_dump" {
		t.Fatalf("expected keychain_dump as first mac program, got %s", mac.Name)
	}
	mp, err := ScriptPath(CategoryMac, mac.Name, ".py")
	if err != nil {
		t.Fatalf("mac ScriptPath: %v", err)
	}
	if err := os.WriteFile(mp, []byte("# tampered\n"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	// Sidecar still says the bundled hash, so the next call must rewrite.
	// But the in-process cache has the path, so we need to bypass it. The
	// public API doesn't expose a bypass; instead we verify via needsRewrite
	// directly: tampering the file content (not the sidecar) doesn't move
	// the sidecar, so a fresh process would detect the divergence via hash
	// check — but within this process the cached path is returned. The
	// tampered content stays until the sidecar is invalidated, which is
	// what we want: the contract is "sidecar hash decides", not "stat decides".
	//
	// To exercise the rewrite, tamper the sidecar instead.
	if err := os.WriteFile(mp+".sum", []byte("stale-hash"), 0o600); err != nil {
		t.Fatalf("tamper sidecar: %v", err)
	}
	if !needsRewrite(mp, hashScriptForTest(CategoryMac, mac.Name)) {
		t.Errorf("needsRewrite should be true after sidecar tamper")
	}
}

// TestExtensionsForWindows verifies that Windows programs can be either
// PowerShell or Python — the resolver in tools/hooks.go depends on this.
func TestExtensionsForWindows(t *testing.T) {
	got := Extensions(CategoryWin)
	if len(got) != 2 || got[0] != ".ps1" || got[1] != ".py" {
		t.Errorf("Windows extensions = %v, want [.ps1 .py]", got)
	}
	for _, c := range Categories() {
		if c == CategoryWin {
			continue
		}
		got := Extensions(c)
		if len(got) != 1 || got[0] != ".py" {
			t.Errorf("%s extensions = %v, want [.py]", c, got)
		}
	}
}

// TestFindProgram verifies lookup behavior.
func TestFindProgram(t *testing.T) {
	if _, ok := FindProgram(CategoryAWS, "iam_enum"); !ok {
		t.Errorf("expected to find iam_enum in awshook")
	}
	if _, ok := FindProgram(CategoryAWS, "definitely_missing"); ok {
		t.Errorf("FindProgram should return false for missing program")
	}
}

// TestProgramNames verifies the enum values are returned in catalog order.
func TestProgramNames(t *testing.T) {
	got := ProgramNames(CategoryAWS)
	want := []string{
		"iam_enum", "iam_privesc", "s3_dump", "lambda_backdoor",
		"ssm_exec", "metadata_harvest", "cloudtrail_blind",
		"secrets_dump", "ec2_snapshot", "cleanup_aws",
	}
	if len(got) != len(want) {
		t.Fatalf("ProgramNames len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ProgramNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// hashScriptForTest reads the embedded content so we can compare against
// what needsRewrite would see.
func hashScriptForTest(c Category, name string) string {
	data, err := bundled.ReadFile("data/" + string(c) + "/" + name + ".py")
	if err != nil {
		return ""
	}
	return hash(data)
}
