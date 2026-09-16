package skills

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// managerState owns the immutable source snapshots shared by every scoped
// Manager handle. Project maps contain only project-source entries; effective
// catalogues are resolved while holding mu rather than copied per scope.
type managerState struct {
	mu     sync.RWMutex
	scanMu sync.Mutex

	opts Options

	bundled     map[string]*Skill
	user        map[string]*Skill
	configured  map[string]*Skill
	projects    map[string]map[string]*Skill
	projectErrs map[string]error
	scopes      map[string]*Manager
	usage       map[string]int
	root        *Manager
	sharedOnly  *Manager

	// startupDir is the normalized logical startup project directory used both
	// by the root handle and as the base for relative session project paths.
	startupDir string
	defaultErr error
	sharedErr  error
}

// Manager is a lightweight view over shared skill source snapshots. An empty
// projectDir denotes the root handle, whose selected project follows the
// state's default. A bound handle keeps its logical project path for life.
type Manager struct {
	state      *managerState
	projectDir string
	sharedOnly bool
}

// NewManager builds a manager over independent writable, bundled, user, and
// project sources. Options are cloned so caller mutations cannot reconfigure it.
func NewManager(opts Options) *Manager {
	opts = cloneOptions(opts)
	startupDir, defaultErr := normalizeStartupProject(opts.ProjectDir)
	opts.ProjectDir = startupDir
	state := &managerState{
		opts:        opts,
		bundled:     map[string]*Skill{},
		user:        map[string]*Skill{},
		configured:  map[string]*Skill{},
		projects:    map[string]map[string]*Skill{},
		projectErrs: map[string]error{},
		scopes:      map[string]*Manager{},
		usage:       map[string]int{},
		startupDir:  startupDir,
		defaultErr:  defaultErr,
	}
	state.root = &Manager{state: state}
	state.sharedOnly = &Manager{state: state, sharedOnly: true}
	return state.root
}

func cloneOptions(opts Options) Options {
	opts.Dirs = append([]string(nil), opts.Dirs...)
	opts.PackDirs = append([]string(nil), opts.PackDirs...)
	return opts
}

func normalizeStartupProject(projectDir string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", nil
	}
	if strings.IndexByte(projectDir, 0) >= 0 {
		return "", fmt.Errorf("normalize startup project directory: path contains NUL")
	}
	logical, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("normalize startup project directory %q: %w", projectDir, err)
	}
	return filepath.Clean(logical), nil
}

// ForProject returns a lightweight catalogue view bound to projectDir. Relative
// paths resolve against the captured startup project. A normalization failure
// returns a shared-only view so callers never accidentally observe the startup
// or another project's skills.
func (m *Manager) ForProject(projectDir string) (*Manager, error) {
	if m == nil {
		return nil, nil
	}
	if strings.TrimSpace(projectDir) == "" {
		m.state.mu.RLock()
		root := m.state.root
		err := m.state.defaultErr
		if err == nil {
			err = m.state.sharedErr
		}
		if err == nil && m.state.startupDir != "" {
			err = m.state.projectErrs[m.state.startupDir]
		}
		m.state.mu.RUnlock()
		return root, err
	}

	logical, err := m.normalizeProject(projectDir)
	if err != nil {
		return m.state.sharedOnly, err
	}

	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	m.state.mu.RLock()
	view, registered := m.state.scopes[logical]
	registeredErr := m.state.sharedErr
	if registeredErr == nil {
		registeredErr = m.state.projectErrs[logical]
	}
	m.state.mu.RUnlock()
	if registered {
		return view, registeredErr
	}

	view = &Manager{state: m.state, projectDir: logical}
	found, scanErr := discoverProject(logical)
	m.state.mu.Lock()
	m.state.projects[logical] = found
	m.state.projectErrs[logical] = scanErr
	m.state.scopes[logical] = view
	sharedErr := m.state.sharedErr
	m.state.mu.Unlock()
	if sharedErr != nil {
		return view, sharedErr
	}
	return view, scanErr
}

func (m *Manager) normalizeProject(projectDir string) (string, error) {
	if strings.IndexByte(projectDir, 0) >= 0 {
		return "", fmt.Errorf("normalize project directory: path contains NUL")
	}
	if filepath.IsAbs(projectDir) {
		return filepath.Clean(projectDir), nil
	}
	m.state.mu.RLock()
	startupDir := m.state.startupDir
	m.state.mu.RUnlock()
	if startupDir == "" {
		return "", fmt.Errorf("normalize relative project directory %q: startup project directory is unavailable", projectDir)
	}
	return filepath.Clean(filepath.Join(startupDir, projectDir)), nil
}

// Reload rescans shared sources once and every registered logical project. All
// successful partial snapshots are published atomically; usage counters remain
// shared and name-keyed.
func (m *Manager) Reload() error {
	if m == nil {
		return nil
	}
	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	return m.reloadLocked()
}

// reloadLocked requires scanMu. Mutation methods use it after writing so they
// do not recursively acquire the scan lock.
func (m *Manager) reloadLocked() error {
	state := m.state
	state.mu.RLock()
	opts := cloneOptions(state.opts)
	projectDirs := make([]string, 0, len(state.projects))
	for projectDir := range state.projects {
		projectDirs = append(projectDirs, projectDir)
	}
	startupDir := state.startupDir
	defaultErr := state.defaultErr
	state.mu.RUnlock()
	if startupDir != "" {
		registered := false
		for _, projectDir := range projectDirs {
			if projectDir == startupDir {
				registered = true
				break
			}
		}
		if !registered {
			projectDirs = append(projectDirs, startupDir)
		}
	}
	sort.Strings(projectDirs)

	bundled, user, configured, sharedErr := discoverShared(opts)
	projects := make(map[string]map[string]*Skill, len(projectDirs))
	projectErrs := make(map[string]error, len(projectDirs))
	firstErr := defaultErr
	if firstErr == nil {
		firstErr = sharedErr
	}
	for _, projectDir := range projectDirs {
		found, err := discoverProject(projectDir)
		projects[projectDir] = found
		projectErrs[projectDir] = err
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}

	state.mu.Lock()
	state.bundled = bundled
	state.user = user
	state.configured = configured
	state.projects = projects
	state.projectErrs = projectErrs
	state.sharedErr = sharedErr
	state.mu.Unlock()
	return firstErr
}

func (m *Manager) selectedProjectLocked() map[string]*Skill {
	if m.sharedOnly {
		return nil
	}
	projectDir := m.projectDir
	if projectDir == "" {
		projectDir = m.state.startupDir
	}
	if projectDir == "" {
		return nil
	}
	return m.state.projects[projectDir]
}

func (m *Manager) effectiveSkillLocked(name string) (*Skill, bool) {
	if skill, ok := m.state.configured[name]; ok {
		return skill, true
	}
	if skill, ok := m.selectedProjectLocked()[name]; ok {
		return skill, true
	}
	if skill, ok := m.state.user[name]; ok {
		return skill, true
	}
	skill, ok := m.state.bundled[name]
	return skill, ok
}

func (m *Manager) effectiveSkillsLocked(yield func(*Skill) bool) {
	layers := [4]map[string]*Skill{m.state.configured, m.selectedProjectLocked(), m.state.user, m.state.bundled}
	for i, layer := range layers {
		for name, skill := range layer {
			shadowed := false
			for _, higher := range layers[:i] {
				if _, exists := higher[name]; exists {
					shadowed = true
					break
				}
			}
			if !shadowed && !yield(skill) {
				return
			}
		}
	}
}

func (m *Manager) effectiveListLocked() []Skill {
	capacity := len(m.state.configured) + len(m.state.user) + len(m.state.bundled) + len(m.selectedProjectLocked())
	out := make([]Skill, 0, capacity)
	for skill := range m.effectiveSkillsLocked {
		out = append(out, cloneSkillWithUsage(skill, m.state.usage[skill.Name]))
	}
	return out
}

func cloneSkillWithUsage(skill *Skill, usage int) Skill {
	clone := cloneSkill(skill)
	clone.UsageCount = usage
	return clone
}
