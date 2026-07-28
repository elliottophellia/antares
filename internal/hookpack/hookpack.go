// Package hookpack ships the offensive-security script library inside the
// binary and unpacks each script to disk on first use.
//
// These are the post-exploitation and vulnerability-testing programs the
// hook tools (attack_script, awshook, azurehook, kubehook, winhook, machook,
// cipipe, ebpf) execute. They are written in Python (PowerShell for a couple
// of Windows programs) and rely only on the standard library plus a few
// well-known third-party packages that the user is prompted to install when
// they are missing.
//
// Scripts are embedded with //go:embed so the binary stays self-contained.
// They are written to ~/.antares/hooks/<category>/<name><ext> lazily — only
// when a tool actually asks for one. Upgrades are detected by hashing the
// embedded content and comparing to a sidecar file, so a new binary always
// wins and an edited on-disk copy is always replaced.
package hookpack

import (
	"crypto/sha256"
	"encoding/hex"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/enowdev/antares/internal/config"
)

//go:embed data/*
var bundled embed.FS

// Category is the directory name under data/ — also the tool that owns it.
type Category string

const (
	CategoryAttackScript Category = "scripts" // attack_script tool
	CategoryAWS          Category = "awshook"
	CategoryAzure        Category = "azurehook"
	CategoryKube         Category = "kubehook"
	CategoryWin          Category = "winhook"
	CategoryMac          Category = "machook"
	CategoryCI           Category = "cipipe"
	CategoryEBPF         Category = "ebpf"
)

// Program describes one bundled script.
type Program struct {
	Name        string // file stem, e.g. "iam_enum"
	Description string // one-line summary, shown to the model
	Args        string // usage hint, e.g. "[--profile PROFILE] [--json-output]"
}

// Catalog is the static catalogue of bundled programs per category. It is
// what each hook tool's schema and "not found" message is built from.
var Catalog = map[Category][]Program{
	CategoryAttackScript: attackScripts,
	CategoryAWS:          awsPrograms,
	CategoryAzure:        azurePrograms,
	CategoryKube:         kubePrograms,
	CategoryWin:          winPrograms,
	CategoryMac:          macPrograms,
	CategoryCI:           ciPrograms,
	CategoryEBPF:         ebpfPrograms,
}

// Categories lists every category, in stable order.
func Categories() []Category {
	return []Category{
		CategoryAttackScript, CategoryAWS, CategoryAzure, CategoryKube,
		CategoryWin, CategoryMac, CategoryCI, CategoryEBPF,
	}
}

// ProgramNames returns the names of every program in a category.
func ProgramNames(c Category) []string {
	progs := Catalog[c]
	out := make([]string, 0, len(progs))
	for _, p := range progs {
		out = append(out, p.Name)
	}
	return out
}

// FindProgram looks up a program in a category by name.
func FindProgram(c Category, name string) (Program, bool) {
	for _, p := range Catalog[c] {
		if p.Name == name {
			return p, true
		}
	}
	return Program{}, false
}

// Extensions lists the file extensions a category uses, in preference order.
// Windows programs can be either PowerShell (.ps1) or Python (.py); every
// other category is Python only.
func Extensions(c Category) []string {
	if c == CategoryWin {
		return []string{".ps1", ".py"}
	}
	return []string{".py"}
}

var (
	mu      sync.Mutex
	cached  = map[string]string{} // key "cat/name.ext" -> extracted path
)

// ScriptPath returns the absolute path to an extracted script, writing it to
// ~/.antares/hooks/<category>/<name><ext> first if it is missing or stale.
// ext is one of Extensions(category) — the caller picks the language. The
// returned path is reused on subsequent calls within the same process.
//
// Staleness is decided by comparing the sha256 of the embedded content to a
// sidecar file at <path>.sum: a missing script, a missing sidecar, or any
// hash mismatch triggers an atomic rewrite. This means a freshly built
// binary with updated scripts always wins over an older on-disk copy,
// without rewriting anything when the scripts have not changed.
func ScriptPath(category Category, name, ext string) (string, error) {
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	key := string(category) + "/" + name + ext

	mu.Lock()
	defer mu.Unlock()
	if p, ok := cached[key]; ok {
		return p, nil
	}

	embedKey := "data/" + key
	data, err := bundled.ReadFile(embedKey)
	if err != nil {
		return "", fmt.Errorf("hookpack: bundled script %s missing: %w", key, err)
	}

	dst := config.Path("hooks", string(category), name+ext)
	sum := hash(data)
	if !needsRewrite(dst, sum) {
		cached[key] = dst
		return dst, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", fmt.Errorf("hookpack: cannot create %s: %w", filepath.Dir(dst), err)
	}
	// Write alongside and rename, so a partial file is never seen.
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o700); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("hookpack: cannot write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("hookpack: cannot install %s: %w", dst, err)
	}
	if err := os.WriteFile(dst+".sum", []byte(sum), 0o600); err != nil {
		// Not fatal — the next run will just rewrite again.
		_ = err
	}
	cached[key] = dst
	return dst, nil
}

// ListBundled returns the names of every file embedded under a category.
// Used by /doctor and the catalogue API to confirm what shipped.
func ListBundled(category Category) []string {
	dir := "data/" + string(category)
	var out []string
	_ = fs.WalkDir(bundled, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, dir+"/")
		out = append(out, rel)
		return nil
	})
	return out
}

// needsRewrite reports whether the script at path should be rewritten.
func needsRewrite(path, sum string) bool {
	stored, err := os.ReadFile(path + ".sum")
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(stored)) != sum
}

// hash returns a short hex digest of the embedded script content. Collision
// safety is not the point — change detection is — so 16 bytes is plenty.
func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:32]
}
