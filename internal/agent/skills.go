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
		if !s.Enabled {
			continue
		}
		out = append(out, tools.SkillInfo{
			Name: s.Name, Description: s.Description,
			Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
		})
	}
	return out
}

func (a skillAdapter) Read(name string) (tools.SkillInfo, string, bool) {
	s, ok := a.m.Get(name)
	if !ok {
		return tools.SkillInfo{}, "", false
	}
	return tools.SkillInfo{
		Name: s.Name, Description: s.Description,
		Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
	}, s.Body, true
}

func (a skillAdapter) Write(name, description, body string, tags []string) error {
	_, err := a.m.Save(name, description, body, tags)
	return err
}

func (a skillAdapter) MarkUsed(name string) { a.m.MarkUsed(name) }

// skillLibrary exposes the manager to tools, or nil when skills are off.
func (a *Agent) skillLibrary() tools.SkillLibrary {
	if a.skills == nil || !a.cfg.Skills.Enabled {
		return nil
	}
	return skillAdapter{m: a.skills}
}
