package agent

import (
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/tools"
)

// skillAdapter bridges the skills manager to the narrow interface tools use,
// keeping the tools package free of a dependency on skills.
type skillAdapter struct{ m *skills.Manager }

func (a skillAdapter) List() []tools.SkillInfo {
	items := a.m.List()
	out := make([]tools.SkillInfo, 0, len(items))
	for _, s := range items {
		// Pack skills are not listed — there are thousands. They are reached
		// with Search instead.
		if !s.Enabled || s.Pack {
			continue
		}
		out = append(out, tools.SkillInfo{
			Name: s.Name, Description: s.Description,
			Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
		})
	}
	return out
}

func (a skillAdapter) Search(query string, limit int) []tools.SkillInfo {
	return infos(a.m.Search(query, limit))
}

func (a skillAdapter) SearchFiltered(query, cwe, tech, category string, limit int) []tools.SkillInfo {
	return infos(a.m.SearchFiltered(query, skills.Filter{CWE: cwe, Tech: tech, Category: category}, limit))
}

func (a skillAdapter) Chains(name string) []tools.SkillInfo {
	return infos(a.m.Chains(name))
}

func (a skillAdapter) Read(name string) (tools.SkillInfo, string, bool) {
	s, ok := a.m.Get(name)
	if !ok {
		return tools.SkillInfo{}, "", false
	}
	return toInfo(*s), s.Body, true
}

// toInfo maps a skills.Skill to the narrow tools.SkillInfo, carrying the
// security metadata through.
func toInfo(s skills.Skill) tools.SkillInfo {
	return tools.SkillInfo{
		Name: s.Name, Description: s.Description,
		Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
		CWEIDs: s.CWEIDs, TechStack: s.TechStack, OWASPID: s.OWASPID, ChainsWith: s.ChainsWith,
	}
}

func infos(items []skills.Skill) []tools.SkillInfo {
	out := make([]tools.SkillInfo, 0, len(items))
	for _, s := range items {
		out = append(out, toInfo(s))
	}
	return out
}

func (a skillAdapter) Write(name, description, body string, tags []string) error {
	_, err := a.m.Save(name, description, body, tags)
	return err
}

func (a skillAdapter) MarkUsed(name string) { a.m.MarkUsed(name) }

// skillLibrary exposes the manager to tools, or nil when skills are off.
func (a *Agent) skillLibrary() tools.SkillLibrary {
	if a.skills == nil || !a.config().Skills.Enabled {
		return nil
	}
	return skillAdapter{m: a.skills}
}
