package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeStateSkill(t *testing.T, dir, file, name, header, body string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	content := "---\nname: " + name + "\n" + header + "---\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDisabledStateIsEffectiveWithoutMutatingSources(t *testing.T) {
	dir := t.TempDir()
	packDir := t.TempDir()
	legacyPath := writeStateSkill(t, dir, "legacy.md", "legacy", "description: legacy description\nenabled: false\n", "legacy body")
	writeStateSkill(t, dir, "active.md", "active", "description: active description\n", "active body")
	writeStateSkill(t, packDir, "pack.md", "pack", "description: pack description\ncategory: web\n", "pack body")

	fixedTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(legacyPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{dir, packDir})
	m.SetPackDirs([]string{packDir})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.LegacyDisabled(); !reflect.DeepEqual(got, []string{"legacy"}) {
		t.Fatalf("LegacyDisabled() = %v, want [legacy]", got)
	}
	if legacy, ok := m.Get("legacy"); !ok || !legacy.Enabled {
		t.Fatalf("legacy enabled state before config overlay = (%+v, %v), want enabled", legacy, ok)
	}

	m.SetDisabled([]string{"legacy", "pack"})

	listed := m.List()
	if got := enabledByName(listed); !reflect.DeepEqual(got, map[string]bool{"active": true, "legacy": false, "pack": false}) {
		t.Fatalf("List enabled state = %v", got)
	}
	legacy, ok := m.Get("legacy")
	if !ok || legacy.Enabled {
		t.Fatalf("Get(legacy) = (%+v, %v), want disabled", legacy, ok)
	}
	library, total := m.Library("web", 0, 10)
	if total != 1 || len(library) != 1 || library[0].Name != "pack" || library[0].Enabled {
		t.Fatalf("Library(web) = (%+v, %d), want disabled pack", library, total)
	}
	if got := m.Count(); got != 1 {
		t.Fatalf("Count() = %d, want only active enabled", got)
	}
	prompt := m.PromptBlock(0)
	if !strings.Contains(prompt, "active: active description") || strings.Contains(prompt, "legacy") || strings.Contains(prompt, "pack") {
		t.Fatalf("PromptBlock() did not reflect effective state: %q", prompt)
	}

	afterBytes, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("SetDisabled changed source bytes:\n%s", afterBytes)
	}
	if afterInfo.Mode() != beforeInfo.Mode() || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("SetDisabled changed source stat: before=%+v after=%+v", beforeInfo, afterInfo)
	}
}

func TestLegacyDisabledUsesSelectedWinnerAndSortsNames(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeStateSkill(t, first, "duplicate.md", "duplicate", "enabled: false\n", "losing legacy body")
	writeStateSkill(t, second, "duplicate.md", "duplicate", "enabled: true\n", "winning body")
	writeStateSkill(t, second, "z.md", "z-name", "enabled: false\n", "z body")
	writeStateSkill(t, second, "a.md", "a-name", "enabled: false\n", "a body")

	m := NewManager([]string{first, second})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.LegacyDisabled(); !reflect.DeepEqual(got, []string{"a-name", "z-name"}) {
		t.Fatalf("LegacyDisabled() = %v, want only sorted selected legacy-disabled names", got)
	}
	winner, ok := m.Get("duplicate")
	if !ok || winner.Body != "winning body" || !winner.Enabled {
		t.Fatalf("selected duplicate = (%+v, %v), want enabled later-directory winner", winner, ok)
	}
}

func TestDisabledPreferenceSurvivesReloadSaveDeleteAndRecreate(t *testing.T) {
	dir := t.TempDir()
	path := writeStateSkill(t, dir, "persistent.md", "persistent", "description: old\nenabled: false\n", "old body")
	m := NewManager([]string{dir})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	m.SetDisabled([]string{"persistent"})

	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	assertSkillDisabled(t, m, "persistent")

	saved, err := m.Save("persistent", "saved", "saved body", []string{"state"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Enabled {
		t.Fatalf("Save returned enabled skill despite retained preference: %+v", saved)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "enabled:") {
		t.Fatalf("Save persisted retired enabled header:\n%s", raw)
	}

	if err := m.Delete("persistent"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("persistent"); ok {
		t.Fatal("deleted skill remains present")
	}
	writeStateSkill(t, dir, "persistent.md", "persistent", "description: recreated\n", "recreated body")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	assertSkillDisabled(t, m, "persistent")
}

func TestSetDisabledClonesInputAndReplacesState(t *testing.T) {
	dir := t.TempDir()
	writeStateSkill(t, dir, "one.md", "one", "", "one body")
	writeStateSkill(t, dir, "two.md", "two", "", "two body")
	m := NewManager([]string{dir})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}

	names := []string{"one"}
	m.SetDisabled(names)
	names[0] = "two"
	assertSkillDisabled(t, m, "one")
	if two, _ := m.Get("two"); !two.Enabled {
		t.Fatal("mutating SetDisabled input changed manager state")
	}

	m.SetDisabled(nil)
	if m.Count() != 2 {
		t.Fatalf("SetDisabled(nil) did not clear state: count=%d", m.Count())
	}
}

func TestReloadReportsErrorAndPublishesReadableSkills(t *testing.T) {
	badRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("blocking file"), 0o644); err != nil {
		t.Fatal(err)
	}
	readable := t.TempDir()
	writeStateSkill(t, readable, "good.md", "good", "description: readable\n", "good body")
	writeStateSkill(t, readable, "malformed.md", "malformed", "tags: [unterminated\n", "bad body")

	m := NewManager([]string{badRoot, readable})
	err := m.Reload()
	if err == nil {
		t.Fatal("Reload() error = nil, want first configured-root error")
	}
	if !strings.Contains(err.Error(), badRoot) {
		t.Fatalf("Reload() error = %q, want first error for %q", err, badRoot)
	}
	good, ok := m.Get("good")
	if !ok || good.Body != "good body" {
		t.Fatalf("readable skill was not published with partial scan: (%+v, %v)", good, ok)
	}
	if _, ok := m.Get("malformed"); ok {
		t.Fatal("malformed skill was published")
	}
}

func enabledByName(skills []Skill) map[string]bool {
	out := make(map[string]bool, len(skills))
	for _, skill := range skills {
		out[skill.Name] = skill.Enabled
	}
	return out
}

func assertSkillDisabled(t *testing.T, m *Manager, name string) {
	t.Helper()
	skill, ok := m.Get(name)
	if !ok || skill.Enabled {
		t.Fatalf("Get(%q) = (%+v, %v), want disabled", name, skill, ok)
	}
}
