package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
)

func writeServerSkill(t *testing.T, dir, name, category, body string) {
	t.Helper()
	content := fmt.Sprintf("---\nname: %s\ndescription: %s description\nenabled: true\ncategory: %s\n---\n%s\n", name, name, category, body)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeServerJSON(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(dst); err != nil {
		t.Fatalf("decode response status %d: %v; body=%q", rr.Code, err, rr.Body.String())
	}
}

func TestSkillConsumersSeeInPlaceReconfigure(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())

	oldDir, oldPack := t.TempDir(), t.TempDir()
	newDir, newPack := t.TempDir(), t.TempDir()
	fallbackDir := t.TempDir()
	writeServerSkill(t, oldDir, "old-catalog", "everyday", "OLD_BODY")
	writeServerSkill(t, oldPack, "old-library", "old-category", "OLD_LIBRARY_BODY")
	writeServerSkill(t, newDir, "new-catalog", "everyday", "NEW_BODY")
	writeServerSkill(t, newPack, "new-library", "new-category", "NEW_LIBRARY_BODY")
	writeServerSkill(t, fallbackDir, "fallback-only", "everyday", "FALLBACK_BODY")

	live := skills.NewManager(skills.Options{Dirs: []string{oldDir}, PackDirs: []string{oldPack}})
	if err := live.Reload(); err != nil {
		t.Fatal(err)
	}
	fallback := skills.NewManager(skills.Options{Dirs: []string{fallbackDir}})
	if err := fallback.Reload(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	ag := &agent.Agent{}
	ag.SetConfig(cfg)
	ag.SetSkills(live)
	s := &Server{
		cfg:    cfg,
		agent:  ag,
		skills: fallback,
		reloadFn: func() error {
			return live.Reconfigure(skills.Options{Dirs: []string{newDir}, PackDirs: []string{newPack}})
		},
	}

	if err := s.applyReload(); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	s.handleListSkills(list, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%q", list.Code, list.Body.String())
	}
	var listed struct {
		Skills []skills.Skill `json:"skills"`
	}
	decodeServerJSON(t, list, &listed)
	if len(listed.Skills) != 1 || listed.Skills[0].Name != "new-catalog" {
		t.Fatalf("list did not expose only the reconfigured everyday catalog: %+v", listed.Skills)
	}

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/skills/new-catalog", nil)
	getReq.SetPathValue("name", "new-catalog")
	s.handleGetSkill(get, getReq)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%q", get.Code, get.Body.String())
	}
	var fetched struct {
		Skill skills.Skill `json:"skill"`
		Body  string       `json:"body"`
	}
	decodeServerJSON(t, get, &fetched)
	if fetched.Skill.Name != "new-catalog" || !strings.Contains(fetched.Body, "NEW_BODY") {
		t.Fatalf("get returned stale skill: %+v body=%q", fetched.Skill, fetched.Body)
	}

	oldGet := httptest.NewRecorder()
	oldGetReq := httptest.NewRequest(http.MethodGet, "/api/skills/old-catalog", nil)
	oldGetReq.SetPathValue("name", "old-catalog")
	s.handleGetSkill(oldGet, oldGetReq)
	if oldGet.Code != http.StatusNotFound {
		t.Fatalf("removed skill status = %d, want 404; body=%q", oldGet.Code, oldGet.Body.String())
	}

	library := httptest.NewRecorder()
	s.handleSkillLibrary(library, httptest.NewRequest(http.MethodGet, "/api/skills/library", nil))
	if library.Code != http.StatusOK {
		t.Fatalf("library status = %d, want 200; body=%q", library.Code, library.Body.String())
	}
	var libraryBody struct {
		Skills []skills.Skill `json:"skills"`
		Total  int            `json:"total"`
	}
	decodeServerJSON(t, library, &libraryBody)
	if libraryBody.Total != 1 || len(libraryBody.Skills) != 1 || libraryBody.Skills[0].Name != "new-library" {
		t.Fatalf("library did not expose only the reconfigured pack: %+v (total %d)", libraryBody.Skills, libraryBody.Total)
	}

	command := httptest.NewRecorder()
	commandReq := httptest.NewRequest(http.MethodPost, "/api/commands/run", strings.NewReader(`{"input":"/skills","surface":"web"}`))
	s.handleCommandRun(command, commandReq)
	if command.Code != http.StatusOK {
		t.Fatalf("command status = %d, want 200; body=%q", command.Code, command.Body.String())
	}
	var commandBody struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeServerJSON(t, command, &commandBody)
	if !commandBody.OK || !strings.Contains(commandBody.Output, "new-catalog") || !strings.Contains(commandBody.Output, "new-library") ||
		strings.Contains(commandBody.Output, "old-catalog") || strings.Contains(commandBody.Output, "old-library") || strings.Contains(commandBody.Output, "fallback-only") {
		t.Fatalf("command did not use the reconfigured live catalog: %+v", commandBody)
	}
}
