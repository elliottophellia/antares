package tools

import (
	"context"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/findings"
	"github.com/enowdev/antares/internal/store"
)

// RAGResult is one retrieved passage.
type RAGResult struct {
	Content string  `json:"content"`
	Path    string  `json:"path,omitempty"`
	DocID   string  `json:"doc_id,omitempty"`
	Score   float64 `json:"score"`
}

// RAGProvider is the retrieval backend (builtin vector store or enowx-rag).
type RAGProvider interface {
	Name() string
	Search(ctx context.Context, collection, query string, topK int) ([]RAGResult, error)
	Index(ctx context.Context, collection string, docs []RAGDoc) (int, error)
	Collections(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, collection string) error
}

// RAGDoc is one document submitted for indexing.
type RAGDoc struct {
	ID      string         `json:"id"`
	Path    string         `json:"path"`
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// SubAgent runs a nested agent turn for delegate_task.
type SubAgent func(ctx context.Context, req SubAgentRequest) (string, error)

// SubAgentRequest describes a delegated workstream.
type SubAgentRequest struct {
	Prompt      string
	SystemExtra string
	Toolset     string
	Model       string
	// Role names a specialist to run this workstream as. It sets the prompt,
	// toolset, and model unless those are given explicitly.
	Role       string
	MaxTurns   int
	ParentID   string
	OnProgress func(Progress)
}

// SkillLibrary is the subset of the skills manager that tools need.
type SkillLibrary interface {
	List() []SkillInfo
	Read(name string) (SkillInfo, string, bool)
	Write(name, description, body string, tags []string) error
	MarkUsed(name string)
}

// SkillInfo describes one stored skill.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Triggers    []string `json:"triggers"`
	Enabled     bool     `json:"enabled"`
}

// Deps bundles the services tools may reach for.
type Deps struct {
	Config *config.Config
	Store  store.Store
	RAG    RAGProvider
	Sub    SubAgent
	Skills SkillLibrary
	// Shell owns persistent terminal sessions keyed by session id.
	Shell *ShellManager
	// Checkpoint saves a copy of a file about to be changed. It may be nil,
	// in which case nothing is kept and an edit cannot be undone.
	Checkpoint func(sessionID, path, tool string)
	// Roles lists the specialist roles available for delegation. It may be nil.
	Roles func() []RoleInfo
	// Findings is the security engagement ledger. It may be nil.
	Findings FindingStore
}

// RoleInfo is the subset of a role the tools need.
type RoleInfo struct {
	Name     string
	Summary  string
	Category string
	Danger   bool
}

// FindingStore is the subset of the findings store the tools need.
type FindingStore interface {
	Add(sessionID string, f findings.Finding) (findings.Finding, error)
}
