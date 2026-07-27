package server

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// routes registers every API endpoint plus the dashboard fallback.
func (s *Server) routes() {
	m := s.mux

	// Health & status
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/status", s.handleStatus)
	m.HandleFunc("GET /api/system/stats", s.handleSystemStats)

	// Chat
	m.HandleFunc("POST /api/chat", s.handleChat)
	m.HandleFunc("POST /api/chat/interrupt", s.handleInterrupt)

	// Sessions
	m.HandleFunc("GET /api/sessions", s.handleListSessions)
	m.HandleFunc("GET /api/sessions/search", s.handleSearchSessions)
	m.HandleFunc("GET /api/sessions/empty/count", s.handleEmptyCount)
	m.HandleFunc("POST /api/sessions/empty/delete", s.handleDeleteEmpty)
	m.HandleFunc("POST /api/sessions/prune", s.handlePruneSessions)
	m.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	m.HandleFunc("GET /api/sessions/{id}/role", s.handleSessionRole)
	m.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	m.HandleFunc("POST /api/sessions/{id}/title", s.handleRenameSession)

	// Setup / onboarding
	m.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	m.HandleFunc("POST /api/setup/test", s.handleSetupTest)
	m.HandleFunc("POST /api/setup/complete", s.handleSetupComplete)

	// Config
	m.HandleFunc("GET /api/config", s.handleGetConfig)
	m.HandleFunc("POST /api/config", s.handleUpdateConfig)
	m.HandleFunc("GET /api/config/raw", s.handleGetRawConfig)
	m.HandleFunc("POST /api/config/raw", s.handleSaveRawConfig)
	m.HandleFunc("GET /api/config/schema", s.handleConfigSchema)

	// Models & providers
	m.HandleFunc("GET /api/model/options", s.handleModelOptions)
	m.HandleFunc("GET /api/model/list", s.handleModelList)
	m.HandleFunc("POST /api/model/set", s.handleModelSet)
	m.HandleFunc("POST /api/providers/{id}/key", s.handleSetProviderKey)

	// Tools
	m.HandleFunc("GET /api/tools", s.handleListTools)
	m.HandleFunc("POST /api/tools/toggle", s.handleToggleTool)
	m.HandleFunc("POST /api/tools/toolset", s.handleSetToolset)

	// Memory
	m.HandleFunc("GET /api/memory", s.handleListMemory)
	m.HandleFunc("POST /api/memory", s.handlePutMemory)
	m.HandleFunc("GET /api/memory/search", s.handleSearchMemory)
	m.HandleFunc("DELETE /api/memory/{id}", s.handleDeleteMemory)
	m.HandleFunc("POST /api/memory/reset", s.handleResetMemory)

	// RAG
	m.HandleFunc("GET /api/rag/status", s.handleRagStatus)
	m.HandleFunc("POST /api/rag/index", s.handleRagIndex)
	m.HandleFunc("POST /api/rag/search", s.handleRagSearch)
	m.HandleFunc("DELETE /api/rag/collections/{name}", s.handleRagDelete)

	// Analytics
	m.HandleFunc("GET /api/analytics", s.handleAnalytics)

	// Logs
	m.HandleFunc("GET /api/logs", s.handleLogs)
	m.HandleFunc("GET /api/logs/stream", s.handleLogStream)

	// Files
	m.HandleFunc("GET /api/files", s.handleListFiles)
	m.HandleFunc("GET /api/files/read", s.handleReadFile)

	// Skills
	m.HandleFunc("GET /api/skills", s.handleListSkills)
	m.HandleFunc("POST /api/skills", s.handleSaveSkill)
	m.HandleFunc("POST /api/skills/toggle", s.handleToggleSkill)
	m.HandleFunc("GET /api/skills/library", s.handleSkillLibrary)
	m.HandleFunc("GET /api/skills/{name}", s.handleGetSkill)
	m.HandleFunc("DELETE /api/skills/{name}", s.handleDeleteSkill)

	// Cron
	m.HandleFunc("GET /api/cron/validate", s.handleValidateCron)
	m.HandleFunc("GET /api/cron/jobs", s.handleListCron)
	m.HandleFunc("POST /api/cron/jobs", s.handleCreateCron)
	m.HandleFunc("DELETE /api/cron/jobs/{id}", s.handleDeleteCron)
	m.HandleFunc("POST /api/cron/jobs/{id}/toggle", s.handleToggleCron)
	m.HandleFunc("POST /api/cron/jobs/{id}/run", s.handleRunCron)
	m.HandleFunc("GET /api/cron/jobs/{id}/runs", s.handleCronRuns)
	// MCP
	m.HandleFunc("GET /api/mcp", s.handleMCPStatus)

	// Roles and the swarm
	m.HandleFunc("GET /api/roles", s.handleRoles)
	m.HandleFunc("GET /api/swarm", s.handleSwarm)

	// Plugins
	m.HandleFunc("GET /api/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/plugins/{name}/toggle", s.handleTogglePlugin)
	m.HandleFunc("POST /api/plugins/reload", s.handleReloadPlugins)

	// Approvals for tools that change something.
	m.HandleFunc("GET /api/approvals", s.handleApprovals)
	m.HandleFunc("POST /api/approvals/{id}", s.handleResolveApproval)

	// Hub: browse and install skills and MCP servers.
	m.HandleFunc("GET /api/hub/skills", s.handleHubSkills)
	m.HandleFunc("POST /api/hub/skills/install", s.handleHubInstallSkill)
	m.HandleFunc("GET /api/hub/mcp", s.handleHubMCP)
	m.HandleFunc("POST /api/hub/mcp/install", s.handleHubInstallMCP)

	// Slash commands, shared with the TUI and the messaging gateways.
	m.HandleFunc("GET /api/commands", s.handleCommandList)
	m.HandleFunc("POST /api/commands/run", s.handleCommandRun)

	// Channels & pairing
	m.HandleFunc("GET /api/channels", s.handleListChannels)
	m.HandleFunc("POST /api/channels/{id}/toggle", s.handleToggleChannel)
	m.HandleFunc("POST /api/channels/{id}/token", s.handleSetChannelToken)
	m.HandleFunc("POST /api/channels/{id}/config", s.handleSetChannelConfig)

	m.HandleFunc("GET /api/autopilot", s.handleAutopilotList)
	m.HandleFunc("POST /api/autopilot", s.handleAutopilotAdd)
	m.HandleFunc("POST /api/autopilot/run", s.handleAutopilotRun)
	m.HandleFunc("DELETE /api/autopilot/{id}", s.handleAutopilotRemove)

	m.HandleFunc("GET /api/engagement/sessions", s.handleEngagementSessions)
	m.HandleFunc("GET /api/engagement", s.handleEngagement)
	m.HandleFunc("POST /api/pairing/approve", s.handleApprovePairing)
	m.HandleFunc("POST /api/pairing/revoke", s.handleRevokePairing)

	// Dashboard (single-page app) — registered last so /api wins.
	m.HandleFunc("/", s.handleDashboard)
}

// handleDashboard serves the embedded SPA, falling back to index.html so client
// routes deep-link correctly.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if s.distFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(devPlaceholder))
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	// Client-side routes have no matching file; serve the SPA entry instead.
	info, err := fs.Stat(s.distFS, path)
	if err != nil || info.IsDir() {
		path = "index.html"
	}

	// Hashed assets are immutable; the entry document must never be cached, or
	// a deploy leaves clients pinned to a stale bundle.
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	// Serving the bytes directly avoids http.FileServer's index.html redirect,
	// which would bounce every SPA route back to "./".
	data, err := fs.ReadFile(s.distFS, path)
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if ctype := mime.TypeByExtension(filepath.Ext(path)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	modTime := time.Time{}
	if info != nil {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, path, modTime, bytes.NewReader(data))
}

const devPlaceholder = `<!doctype html>
<meta charset="utf-8">
<title>Antares API</title>
<style>
 body{font-family:ui-sans-serif,system-ui,sans-serif;background:#161011;color:#eee;
      display:grid;place-items:center;min-height:100vh;margin:0;text-align:center;padding:2rem}
 code{background:#242427;padding:.15rem .4rem;border-radius:.3rem;font-size:.9em}
 a{color:#e05a4e}
</style>
<div>
  <img src="/antares-192.png" alt="" width="72" height="72" style="opacity:.9">
  <h1>Antares API</h1>
  <p>The backend is running. No dashboard is embedded in this binary.</p>
  <p>Development: run <code>make dev-web</code> and open port <code>5173</code>.</p>
  <p>Production: <code>make build</code> embeds the dashboard into the binary.</p>
</div>`
