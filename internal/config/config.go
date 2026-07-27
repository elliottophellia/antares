// Package config holds the layered Antares configuration: file defaults,
// YAML overrides from the profile config, then environment variables.
package config

// Config is the complete runtime configuration tree.
type Config struct {
	Model         Model               `yaml:"model" json:"model"`
	Providers     map[string]Provider `yaml:"providers" json:"providers"`
	Database      Database            `yaml:"database" json:"database"`
	Server        Server              `yaml:"server" json:"server"`
	Agent         Agent               `yaml:"agent" json:"agent"`
	Tools         Tools               `yaml:"tools" json:"tools"`
	Terminal      Terminal            `yaml:"terminal" json:"terminal"`
	Compression   Compression         `yaml:"compression" json:"compression"`
	PromptCaching PromptCaching       `yaml:"prompt_caching" json:"prompt_caching"`
	Memory        Memory              `yaml:"memory" json:"memory"`
	RAG           RAG                 `yaml:"rag" json:"rag"`
	Skills        Skills              `yaml:"skills" json:"skills"`
	Plugins       Plugins             `yaml:"plugins" json:"plugins"`
	Roles         Roles               `yaml:"roles" json:"roles"`
	Security      Security            `yaml:"security" json:"security"`
	ImageGen      ImageGen            `yaml:"image_gen" json:"image_gen"`
	Cron          Cron                `yaml:"cron" json:"cron"`
	Gateway       Gateway             `yaml:"gateway" json:"gateway"`
	Delegation    Delegation          `yaml:"delegation" json:"delegation"`
	CodeExecution CodeExecution       `yaml:"code_execution" json:"code_execution"`
	Guardrails    Guardrails          `yaml:"tool_loop_guardrails" json:"tool_loop_guardrails"`
	SessionReset  SessionReset        `yaml:"session_reset" json:"session_reset"`
	Streaming     Streaming           `yaml:"streaming" json:"streaming"`
	Display       Display             `yaml:"display" json:"display"`
	Logging       Logging             `yaml:"logging" json:"logging"`
	MCP           MCP                 `yaml:"mcp" json:"mcp"`

	MaxConcurrentSessions int  `yaml:"max_concurrent_sessions" json:"max_concurrent_sessions"`
	GroupSessionsPerUser  bool `yaml:"group_sessions_per_user" json:"group_sessions_per_user"`
}

// Model selects which LLM answers by default.
type Model struct {
	Default   string `yaml:"default" json:"default"`
	Provider  string `yaml:"provider" json:"provider"`
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key" json:"api_key"`
	Auxiliary string `yaml:"auxiliary" json:"auxiliary"`
	Vision    string `yaml:"vision" json:"vision"`
	Embedding string `yaml:"embedding" json:"embedding"`

	Temperature      float64 `yaml:"temperature" json:"temperature"`
	TopP             float64 `yaml:"top_p" json:"top_p"`
	MaxTokens        int     `yaml:"max_tokens" json:"max_tokens"`
	ContextWindow    int     `yaml:"context_window" json:"context_window"`
	ReasoningEffort  string  `yaml:"reasoning_effort" json:"reasoning_effort"`
	ParallelToolCall bool    `yaml:"parallel_tool_calls" json:"parallel_tool_calls"`
	// MaxRetries is how many times a transient provider failure (429, 5xx,
	// dropped connection) is retried with backoff. Zero uses a safe default.
	MaxRetries int `yaml:"max_retries" json:"max_retries"`
	// Fallback lists models to try, in order, when the primary fails after its
	// retries — each a bare model id or "provider/model". A whole provider
	// outage or a decommissioned model then degrades instead of erroring.
	Fallback []string `yaml:"fallback" json:"fallback"`
	// Panel is the set of models /panel asks. Two or more, or the command has
	// nothing to compare.
	Panel []string `yaml:"panel" json:"panel"`
}

// Provider is one configured LLM endpoint.
type Provider struct {
	Kind        string            `yaml:"kind" json:"kind"` // openai-compatible|anthropic|openai|gemini|custom
	BaseURL     string            `yaml:"base_url" json:"base_url"`
	APIKey      string            `yaml:"api_key" json:"api_key"`
	APIKeyEnv   string            `yaml:"api_key_env" json:"api_key_env"`
	Headers     map[string]string `yaml:"headers" json:"headers"`
	Models      []string          `yaml:"models" json:"models"`
	Enabled     bool              `yaml:"enabled" json:"enabled"`
	Label       string            `yaml:"label" json:"label"`
	TimeoutSecs int               `yaml:"timeout_seconds" json:"timeout_seconds"`
	APIVersion  string            `yaml:"api_version" json:"api_version"`
	Region      string            `yaml:"region" json:"region"`
}

// Database picks the persistence driver.
type Database struct {
	Driver   string `yaml:"driver" json:"driver"` // sqlite|postgres|memory
	DSN      string `yaml:"dsn" json:"dsn"`
	MaxConns int    `yaml:"max_conns" json:"max_conns"`
	Busy     int    `yaml:"busy_timeout_ms" json:"busy_timeout_ms"`
	WAL      bool   `yaml:"wal" json:"wal"`
}

// Server configures the HTTP API + dashboard host.
type Server struct {
	Host         string   `yaml:"host" json:"host"`
	Port         int      `yaml:"port" json:"port"`
	AuthToken    string   `yaml:"auth_token" json:"auth_token"`
	AuthDisabled bool     `yaml:"auth_disabled" json:"auth_disabled"`
	CORSOrigins  []string `yaml:"cors_origins" json:"cors_origins"`
	PublicURL    string   `yaml:"public_url" json:"public_url"`
	TrustProxy   bool     `yaml:"trust_proxy" json:"trust_proxy"`
}

// Agent tunes the conversation loop.
type Agent struct {
	MaxTurns          int      `yaml:"max_turns" json:"max_turns"`
	MaxToolCalls      int      `yaml:"max_tool_calls_per_turn" json:"max_tool_calls_per_turn"`
	Verbose           bool     `yaml:"verbose" json:"verbose"`
	ReasoningEffort   string   `yaml:"reasoning_effort" json:"reasoning_effort"`
	Personality       string   `yaml:"personality" json:"personality"`
	SystemPromptExtra string   `yaml:"system_prompt_extra" json:"system_prompt_extra"`
	Workspace         string   `yaml:"workspace" json:"workspace"`
	Timezone          string   `yaml:"timezone" json:"timezone"`
	Language          string   `yaml:"language" json:"language"`
	IdleTimeoutSecs   int      `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
	StopSequences     []string `yaml:"stop_sequences" json:"stop_sequences"`

	// RepeatLimit is how many identical tool calls are tolerated before the
	// agent is told to change approach. Zero uses the default.
	RepeatLimit int `yaml:"repeat_limit" json:"repeat_limit"`
	// VerifyReplies runs a cheap second model over a finished answer to catch
	// work that was described but not done.
	VerifyReplies bool `yaml:"verify_replies" json:"verify_replies"`
	// VerifyMax bounds how many times one turn can be sent back for more work.
	VerifyMax int `yaml:"verify_max" json:"verify_max"`
	// GoalMaxIterations bounds a standing goal so it cannot run forever.
	GoalMaxIterations int `yaml:"goal_max_iterations" json:"goal_max_iterations"`
	// WrapUntrustedOutput fences tool output that carries external content
	// (web pages, HTTP bodies, MCP results) so the model treats it as data and
	// not as instructions — a defence against prompt injection. On by default.
	WrapUntrustedOutput bool `yaml:"wrap_untrusted_output" json:"wrap_untrusted_output"`
	// SmartTitles names a new conversation with a short model-written title
	// instead of a truncation of the first message. On by default; it costs one
	// cheap auxiliary-model call per session.
	SmartTitles bool `yaml:"smart_titles" json:"smart_titles"`
}

// Tools controls which toolsets are exposed to the model.
type Tools struct {
	Enabled        []string          `yaml:"enabled" json:"enabled"`
	Disabled       []string          `yaml:"disabled" json:"disabled"`
	Toolset        string            `yaml:"toolset" json:"toolset"`
	ApprovalMode   string            `yaml:"approval_mode" json:"approval_mode"` // auto|prompt|deny
	MaxOutputChars int               `yaml:"max_output_chars" json:"max_output_chars"`
	Timeouts       map[string]int    `yaml:"timeouts" json:"timeouts"`
	WebSearch      WebSearch         `yaml:"web_search" json:"web_search"`
	Browser        Browser           `yaml:"browser" json:"browser"`
	HTTP           HTTP              `yaml:"http" json:"http"`
	Platform       map[string]string `yaml:"platform_toolsets" json:"platform_toolsets"`
}

// HTTP configures the http_request tool, which calls HTTP APIs with a
// browser-identical TLS/HTTP2 fingerprint so requests are not rejected by
// bot-detection layers at the cryptographic level.
type HTTP struct {
	// Preset is the browser fingerprint to mimic, e.g. "chrome-131" (the
	// default), "chrome-131-windows", "chrome-133". Empty uses the default.
	Preset string `yaml:"preset" json:"preset"`
	// Proxy routes requests through a proxy, e.g. "http://host:3128" or
	// "socks5://host:1080".
	Proxy string `yaml:"proxy" json:"proxy"`
	// TimeoutSeconds bounds a single request; 0 uses the built-in default.
	TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds"`
	// WrapTerminal puts curl/wget shims on the terminal's PATH so those
	// commands go through the fingerprinted client too, falling back to the
	// real binary for anything the shim cannot handle. On by default.
	WrapTerminal bool `yaml:"wrap_terminal" json:"wrap_terminal"`
}

// WebSearch configures the search backend used by the web_search tool.
// Browser configures the real-browser tool.
type Browser struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Executable overrides Chrome discovery; empty means look for one.
	Executable string `yaml:"executable" json:"executable"`
	// RemoteURL attaches to a Chrome already running with a debugging port,
	// e.g. http://127.0.0.1:9222, instead of starting one.
	RemoteURL string `yaml:"remote_url" json:"remote_url"`
	// UserDataDir keeps cookies and logins between runs.
	UserDataDir string `yaml:"user_data_dir" json:"user_data_dir"`
	// Headed shows the window; it needs a display to be of any use.
	Headed bool `yaml:"headed" json:"headed"`
	Width  int  `yaml:"width" json:"width"`
	Height int  `yaml:"height" json:"height"`
	// Stealth launches a source-patched anti-detection Chromium (downloaded and
	// verified on first use) instead of the system Chrome, so pages guarded by
	// bot-detection challenges — Cloudflare Turnstile and the like — load. The
	// binary is fetched once and cached; it carries its own upstream license.
	Stealth bool `yaml:"stealth" json:"stealth"`
	// Proxy routes the stealth browser through a proxy, e.g. "http://host:3128"
	// or "socks5://host:1080".
	Proxy string `yaml:"proxy" json:"proxy"`
	// Timezone and Locale spoof the stealth fingerprint, e.g. "America/New_York"
	// and "en-US".
	Timezone string `yaml:"timezone" json:"timezone"`
	Locale   string `yaml:"locale" json:"locale"`
}

type WebSearch struct {
	Provider   string `yaml:"provider" json:"provider"` // duckduckgo|brave|tavily|searxng|none
	APIKey     string `yaml:"api_key" json:"api_key"`
	BaseURL    string `yaml:"base_url" json:"base_url"`
	MaxResults int    `yaml:"max_results" json:"max_results"`
}

// Terminal configures the shell execution backend.
type Terminal struct {
	Backend         string   `yaml:"backend" json:"backend"` // local|docker|ssh
	CWD             string   `yaml:"cwd" json:"cwd"`
	Timeout         int      `yaml:"timeout" json:"timeout"`
	HomeMode        string   `yaml:"home_mode" json:"home_mode"`
	LifetimeSeconds int      `yaml:"lifetime_seconds" json:"lifetime_seconds"`
	Shell           string   `yaml:"shell" json:"shell"`
	BlockedCommands []string `yaml:"blocked_commands" json:"blocked_commands"`
	AllowNetwork    bool     `yaml:"allow_network" json:"allow_network"`
	// Sandbox confines what a command can reach: none, auto, bubblewrap, or
	// namespace. Auto uses the strongest thing this machine has.
	Sandbox string `yaml:"sandbox" json:"sandbox"`
	// SandboxHidden are paths kept out of the sandbox. Empty uses the default
	// list, which is the credential directories.
	SandboxHidden []string `yaml:"sandbox_hidden" json:"sandbox_hidden"`
	DockerImage   string   `yaml:"docker_image" json:"docker_image"`
	SSHHost       string   `yaml:"ssh_host" json:"ssh_host"`
}

// Compression controls automatic context compaction.
type Compression struct {
	Enabled                bool    `yaml:"enabled" json:"enabled"`
	ProgressNotices        bool    `yaml:"progress_notices" json:"progress_notices"`
	Threshold              float64 `yaml:"threshold" json:"threshold"`
	TargetRatio            float64 `yaml:"target_ratio" json:"target_ratio"`
	ProtectLastN           int     `yaml:"protect_last_n" json:"protect_last_n"`
	ProtectFirstN          int     `yaml:"protect_first_n" json:"protect_first_n"`
	MinTailUserMessages    int     `yaml:"min_tail_user_messages" json:"min_tail_user_messages"`
	MaxAttempts            int     `yaml:"max_attempts" json:"max_attempts"`
	IdleCompactAfterSecs   int     `yaml:"idle_compact_after_seconds" json:"idle_compact_after_seconds"`
	ProactivePruneTokens   int     `yaml:"proactive_prune_tokens" json:"proactive_prune_tokens"`
	ProactivePruneMinChars int     `yaml:"proactive_prune_min_result_chars" json:"proactive_prune_min_result_chars"`
}

// PromptCaching mirrors provider-side cache controls.
type PromptCaching struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CacheTTL string `yaml:"cache_ttl" json:"cache_ttl"`
}

// Memory is the agent-curated long-term memory.
type Memory struct {
	Enabled            bool `yaml:"memory_enabled" json:"memory_enabled"`
	UserProfileEnabled bool `yaml:"user_profile_enabled" json:"user_profile_enabled"`
	MemoryCharLimit    int  `yaml:"memory_char_limit" json:"memory_char_limit"`
	UserCharLimit      int  `yaml:"user_char_limit" json:"user_char_limit"`
	NudgeInterval      int  `yaml:"nudge_interval" json:"nudge_interval"`
	FlushMinTurns      int  `yaml:"flush_min_turns" json:"flush_min_turns"`
	SearchLimit        int  `yaml:"search_limit" json:"search_limit"`
}

// RAG selects the retrieval backend used for semantic recall.
type RAG struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Provider string `yaml:"provider" json:"provider"` // builtin|enowx|none

	// Builtin vector store settings.
	EmbedProvider string `yaml:"embed_provider" json:"embed_provider"`
	EmbedModel    string `yaml:"embed_model" json:"embed_model"`
	EmbedBaseURL  string `yaml:"embed_base_url" json:"embed_base_url"`
	EmbedAPIKey   string `yaml:"embed_api_key" json:"embed_api_key"`
	ChunkSize     int    `yaml:"chunk_size" json:"chunk_size"`
	ChunkOverlap  int    `yaml:"chunk_overlap" json:"chunk_overlap"`
	TopK          int    `yaml:"top_k" json:"top_k"`
	Hybrid        bool   `yaml:"hybrid" json:"hybrid"`

	// enowx-rag daemon settings.
	EnowxBaseURL  string `yaml:"enowx_base_url" json:"enowx_base_url"`
	EnowxToken    string `yaml:"enowx_token" json:"enowx_token"`
	EnowxProject  string `yaml:"enowx_project" json:"enowx_project"`
	EnowxRerank   bool   `yaml:"enowx_rerank" json:"enowx_rerank"`
	EnowxCompress bool   `yaml:"enowx_compress" json:"enowx_compress"`
}

// ImageGen configures text-to-image generation.
type ImageGen struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Provider string `yaml:"provider" json:"provider"` // a configured provider id, or "openai"
	Model    string `yaml:"model" json:"model"`
	BaseURL  string `yaml:"base_url" json:"base_url"`
	APIKey   string `yaml:"api_key" json:"api_key"`
	Size     string `yaml:"size" json:"size"`
}

// Security holds settings for the authorized-security roles.
type Security struct {
	// Scope is the list of targets security roles may test: exact hosts,
	// wildcard domains (*.example.com), or CIDR ranges. Empty authorizes
	// nothing — the tools fail closed.
	Scope []string `yaml:"scope" json:"scope"`
	// RequireScope, when true, makes the scope_check tool refuse rather than
	// warn on an out-of-scope target.
	RequireScope bool `yaml:"require_scope" json:"require_scope"`
}

// Roles configures the specialist agent roles.
type Roles struct {
	Dirs []string `yaml:"dirs" json:"dirs"`
}

// Plugins configures external programs that hook into the agent.
type Plugins struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Dirs    []string `yaml:"dirs" json:"dirs"`
}

// Skills configures the self-improving skill library.
type Skills struct {
	Enabled               bool     `yaml:"enabled" json:"enabled"`
	Dirs                  []string `yaml:"dirs" json:"dirs"`
	CreationNudgeInterval int      `yaml:"creation_nudge_interval" json:"creation_nudge_interval"`
	AutoCreate            bool     `yaml:"auto_create" json:"auto_create"`
	HubSources            []string `yaml:"hub_sources" json:"hub_sources"`
}

// Cron configures the built-in scheduler.
type Cron struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Timezone      string `yaml:"timezone" json:"timezone"`
	MaxConcurrent int    `yaml:"max_concurrent" json:"max_concurrent"`
	HistoryLimit  int    `yaml:"history_limit" json:"history_limit"`
}

// Gateway holds messaging platform credentials.
type Gateway struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Telegram Telegram `yaml:"telegram" json:"telegram"`
	Discord  Discord  `yaml:"discord" json:"discord"`
}

// Telegram bot settings.
type Telegram struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	BotToken       string   `yaml:"bot_token" json:"bot_token"`
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedChats   []string `yaml:"allowed_chats" json:"allowed_chats"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
	StreamEdits    bool     `yaml:"stream_edits" json:"stream_edits"`
}

// Discord bot settings.
type Discord struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	BotToken       string   `yaml:"bot_token" json:"bot_token"`
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedGuilds  []string `yaml:"allowed_guilds" json:"allowed_guilds"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
	Intents        int      `yaml:"intents" json:"intents"`
}

// Delegation bounds subagent recursion.
type Delegation struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxIterations int  `yaml:"max_iterations" json:"max_iterations"`
	MaxDepth      int  `yaml:"max_depth" json:"max_depth"`
	MaxParallel   int  `yaml:"max_parallel" json:"max_parallel"`
}

// CodeExecution bounds the sandboxed code tool.
type CodeExecution struct {
	Enabled      bool `yaml:"enabled" json:"enabled"`
	Timeout      int  `yaml:"timeout" json:"timeout"`
	MaxToolCalls int  `yaml:"max_tool_calls" json:"max_tool_calls"`
}

// Guardrails detects and stops runaway tool loops.
type Guardrails struct {
	WarningsEnabled bool `yaml:"warnings_enabled" json:"warnings_enabled"`
	HardStopEnabled bool `yaml:"hard_stop_enabled" json:"hard_stop_enabled"`
	WarnAfter       int  `yaml:"warn_after" json:"warn_after"`
	HardStopAfter   int  `yaml:"hard_stop_after" json:"hard_stop_after"`
}

// SessionReset controls automatic session rotation.
type SessionReset struct {
	Mode        string `yaml:"mode" json:"mode"` // never|idle|daily
	IdleMinutes int    `yaml:"idle_minutes" json:"idle_minutes"`
	AtHour      int    `yaml:"at_hour" json:"at_hour"`
}

// Streaming toggles incremental responses.
type Streaming struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Display holds UI preferences shared by the dashboard.
type Display struct {
	Compact          bool   `yaml:"compact" json:"compact"`
	ToolProgress     bool   `yaml:"tool_progress" json:"tool_progress"`
	ShowReasoning    bool   `yaml:"show_reasoning" json:"show_reasoning"`
	Theme            string `yaml:"theme" json:"theme"`
	Skin             string `yaml:"skin" json:"skin"`
	Language         string `yaml:"language" json:"language"`
	BellOnComplete   bool   `yaml:"bell_on_complete" json:"bell_on_complete"`
	InterimAssistant bool   `yaml:"interim_assistant_messages" json:"interim_assistant_messages"`
}

// Logging controls log level and sinks.
type Logging struct {
	Level string `yaml:"level" json:"level"`
	File  string `yaml:"file" json:"file"`
	JSON  bool   `yaml:"json" json:"json"`
}

// MCP holds Model Context Protocol server registrations.
type MCP struct {
	Enabled bool                 `yaml:"enabled" json:"enabled"`
	Servers map[string]MCPServer `yaml:"servers" json:"servers"`
}

// MCPServer is one registered MCP server (stdio or http transport).
type MCPServer struct {
	Transport string            `yaml:"transport" json:"transport"` // stdio|http
	Command   string            `yaml:"command" json:"command"`
	Args      []string          `yaml:"args" json:"args"`
	Env       map[string]string `yaml:"env" json:"env"`
	URL       string            `yaml:"url" json:"url"`
	Headers   map[string]string `yaml:"headers" json:"headers"`
	Enabled   bool              `yaml:"enabled" json:"enabled"`
}
