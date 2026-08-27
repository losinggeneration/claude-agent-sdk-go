package claudeagentsdk

import "encoding/json"

// PermissionMode controls how Claude Code handles tool permissions.
type PermissionMode string

const (
	PermissionDefault           PermissionMode = "default"
	PermissionAcceptEdits       PermissionMode = "acceptEdits"
	PermissionPlan              PermissionMode = "plan"
	PermissionBypassPermissions PermissionMode = "bypassPermissions"
	PermissionDontAsk           PermissionMode = "dontAsk"
	PermissionAuto              PermissionMode = "auto"
)

// Options configures a Claude Code session.
type Options struct {
	Tools                   []string
	AllowedTools            []string
	DisallowedTools         []string
	SystemPrompt            string
	AppendSystemPrompt      string
	SystemPromptFile        string
	MCPServers              map[string]any
	StrictMCPConfig         bool
	PermissionMode          PermissionMode
	PermissionPromptTool    string
	ContinueConversation    bool
	Resume                  string
	SessionID               string
	MaxTurns                int
	MaxBudgetUSD            float64
	Model                   string
	FallbackModel           string
	Betas                   []string
	CWD                     string
	CLIPath                 string
	Settings                string
	AddDirs                 []string
	Env                     map[string]string
	ExtraArgs               map[string]*string
	MaxBufferSize           int
	Stderr                  func(string)
	User                    string
	IncludePartialMessages  bool
	IncludeHookEvents       bool
	ForkSession             bool
	ResumeSessionAt         string
	ResumeDropsTurn         *string
	SettingSources          []string
	Plugins                 []PluginConfig
	MaxThinkingTokens       *int
	Thinking                *ThinkingConfig
	Effort                  string
	OutputFormat            map[string]any
	EnableFileCheckpointing bool
	TaskBudgetTotal         int
	Agents                  map[string]AgentDefinition
	Skills                  any // nil, []string, or "all"
	ForwardSubagentText     bool
	Sandbox                 map[string]any

	CanUseTool        CanUseTool
	Hooks             map[string][]HookMatcher
	SessionStore      SessionStore
	SessionStoreFlush string // batched or eager
	LoadTimeoutMS     int
}

type PluginConfig struct {
	Type string
	Path string
}

type AgentDefinition struct {
	Description     string
	Prompt          string
	Tools           []string
	DisallowedTools []string
	Model           string
	Skills          []string
	Memory          string
	MCPServers      []any
	InitialPrompt   string
	MaxTurns        *int
	Background      *bool
	Effort          any
	PermissionMode  PermissionMode
}

type ThinkingConfig struct {
	Type         string // adaptive, enabled, disabled
	BudgetTokens int
	Display      string
}

// Message is any parsed SDK message.
type Message interface{ message() }

type ContentBlock interface{ contentBlock() }

type TextBlock struct{ Text string }

func (TextBlock) contentBlock() {}

type ThinkingBlock struct{ Thinking, Signature string }

func (ThinkingBlock) contentBlock() {}

type ToolUseBlock struct {
	ID    string
	Name  string
	Input map[string]any
}

func (ToolUseBlock) contentBlock() {}

type ToolResultBlock struct {
	ToolUseID string
	Content   any
	IsError   *bool
}

func (ToolResultBlock) contentBlock() {}

type ServerToolUseBlock struct {
	ID    string
	Name  string
	Input map[string]any
}

func (ServerToolUseBlock) contentBlock() {}

type ServerToolResultBlock struct {
	ToolUseID string
	Content   map[string]any
}

func (ServerToolResultBlock) contentBlock() {}

type UserMessage struct {
	Content         any // string or []ContentBlock
	UUID            string
	ParentToolUseID string
	ToolUseResult   map[string]any
	Origin          map[string]any
}

func (UserMessage) message() {}

type AssistantMessage struct {
	Content         []ContentBlock
	Model           string
	ParentToolUseID string
	Error           string
	Usage           map[string]any
	MessageID       string
	StopReason      string
	SessionID       string
	UUID            string
}

func (AssistantMessage) message() {}

type SystemMessage struct {
	Subtype string
	Data    map[string]any
}

func (SystemMessage) message() {}

type TaskStartedMessage struct {
	SystemMessage
	TaskID, Description, UUID, SessionID string
	ToolUseID, TaskType                  string
}

type TaskProgressMessage struct {
	SystemMessage
	TaskID, Description, UUID, SessionID string
	Usage                                map[string]any
	ToolUseID, LastToolName              string
}

type TaskNotificationMessage struct {
	SystemMessage
	TaskID, Status, OutputFile, Summary, UUID, SessionID string
	ToolUseID                                            string
	Usage                                                map[string]any
}

type TaskUpdatedMessage struct {
	SystemMessage
	TaskID, Status, SessionID, UUID string
	Patch                           map[string]any
}

type MirrorErrorMessage struct {
	SystemMessage
	Key   map[string]any
	Error string
}

type ResultMessage struct {
	Subtype           string
	DurationMS        int
	DurationAPIMS     int
	IsError           bool
	NumTurns          int
	SessionID         string
	StopReason        string
	TotalCostUSD      *float64
	Usage             map[string]any
	Result            string
	StructuredOutput  any
	ModelUsage        map[string]any
	PermissionDenials []any
	DeferredToolUse   *DeferredToolUse
	Errors            []string
	APIErrorStatus    *int
	UUID              string
	TerminalReason    string
	Origin            map[string]any
}

func (ResultMessage) message() {}

type DeferredToolUse struct {
	ID    string
	Name  string
	Input map[string]any
}

type StreamEvent struct {
	UUID            string
	SessionID       string
	Event           map[string]any
	ParentToolUseID string
}

func (StreamEvent) message() {}

type RateLimitEvent struct {
	RateLimitInfo RateLimitInfo
	UUID          string
	SessionID     string
}

func (RateLimitEvent) message() {}

type RateLimitInfo struct {
	Status                string
	ResetsAt              *int64
	RateLimitType         string
	Utilization           *float64
	OverageStatus         string
	OverageResetsAt       *int64
	OverageDisabledReason string
	Raw                   map[string]any
}

type ConversationResetMessage struct {
	NewConversationID string
	UUID              string
	SessionID         string
}

func (ConversationResetMessage) message() {}

type HookEventMessage struct {
	SystemMessage
	HookEventName string
	SessionID     string
	UUID          string
}

type RawMessage map[string]any

func (RawMessage) message() {}

// Typed option/result structs mirroring the Python TypedDict/dataclass surface.
// Field names match the Python/TypeScript wire format so the transport code
// can pass them straight through.

type SettingSource string

const (
	SettingSourceUser    SettingSource = "user"
	SettingSourceProject SettingSource = "project"
	SettingSourceLocal   SettingSource = "local"
)

type EffortLevel string

const (
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
	EffortXHigh  EffortLevel = "xhigh"
	EffortMax    EffortLevel = "max"
)

type ThinkingDisplay string

const (
	ThinkingDisplaySummarized ThinkingDisplay = "summarized"
	ThinkingDisplayOmitted    ThinkingDisplay = "omitted"
)

type ThinkingConfigAdaptive struct {
	Type    string          `json:"type"`
	Display ThinkingDisplay `json:"display,omitempty"`
}

type ThinkingConfigEnabled struct {
	Type         string          `json:"type"`
	BudgetTokens int             `json:"budget_tokens"`
	Display      ThinkingDisplay `json:"display,omitempty"`
}

type ThinkingConfigDisabled struct {
	Type string `json:"type"`
}

type TaskBudget struct {
	Total int `json:"total"`
}

type ToolsPreset struct {
	Type   string `json:"type"`
	Preset string `json:"preset"`
}

var ToolsPresetClaudeCode = ToolsPreset{Type: "preset", Preset: "claude_code"}

type SystemPromptPreset struct {
	Type                   string `json:"type"`
	Preset                 string `json:"preset"`
	Append                 string `json:"append,omitempty"`
	ExcludeDynamicSections bool   `json:"exclude_dynamic_sections,omitempty"`
}

type SystemPromptFile struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type SandboxNetworkConfig struct {
	AllowedDomains          []string `json:"allowedDomains,omitempty"`
	DeniedDomains           []string `json:"deniedDomains,omitempty"`
	AllowManagedDomainsOnly bool     `json:"allowManagedDomainsOnly,omitempty"`
	AllowUnixSockets        []string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets     bool     `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding       bool     `json:"allowLocalBinding,omitempty"`
	AllowMachLookup         []string `json:"allowMachLookup,omitempty"`
	HTTPProxyPort           int      `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort          int      `json:"socksProxyPort,omitempty"`
}

type SandboxIgnoreViolations struct {
	File    []string `json:"file,omitempty"`
	Network []string `json:"network,omitempty"`
}

type SandboxSettings struct {
	Enabled                   bool                     `json:"enabled,omitempty"`
	AutoAllowBashIfSandboxed  bool                     `json:"autoAllowBashIfSandboxed,omitempty"`
	ExcludedCommands          []string                 `json:"excludedCommands,omitempty"`
	AllowUnsandboxedCommands  bool                     `json:"allowUnsandboxedCommands,omitempty"`
	Network                   *SandboxNetworkConfig    `json:"network,omitempty"`
	IgnoreViolations          *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox bool                     `json:"enableWeakerNestedSandbox,omitempty"`
}

type SdkPluginConfig struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type SdkBeta = string

const (
	BetaContext1M SdkBeta = "context-1m-2025-08-07"
)

type McpStdioServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type McpSSEServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type McpHttpServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type McpSdkServerConfig struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Instance *Server `json:"-"`
}

type McpServerConfig interface{ mcpConfig() }

func (McpStdioServerConfig) mcpConfig() {}
func (McpSSEServerConfig) mcpConfig()   {}
func (McpHttpServerConfig) mcpConfig()  {}
func (McpSdkServerConfig) mcpConfig()   {}

type McpSdkServerConfigStatus struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type McpClaudeAIProxyServerConfig struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	ID   string `json:"id"`
}

type McpToolAnnotations struct {
	ReadOnly    bool `json:"readOnlyHint,omitempty"`
	Destructive bool `json:"destructiveHint,omitempty"`
	OpenWorld   bool `json:"openWorldHint,omitempty"`
}

type McpToolInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Annotations *McpToolAnnotations `json:"annotations,omitempty"`
}

type McpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type McpServerConnectionStatus = string

const (
	McpStatusConnected McpServerConnectionStatus = "connected"
	McpStatusFailed    McpServerConnectionStatus = "failed"
	McpStatusNeedsAuth McpServerConnectionStatus = "needs-auth"
	McpStatusPending   McpServerConnectionStatus = "pending"
	McpStatusDisabled  McpServerConnectionStatus = "disabled"
)

type McpServerStatus struct {
	Name       string                    `json:"name"`
	Status     McpServerConnectionStatus `json:"status"`
	ServerInfo *McpServerInfo            `json:"serverInfo,omitempty"`
	Error      string                    `json:"error,omitempty"`
	Config     map[string]any            `json:"config,omitempty"`
	Scope      string                    `json:"scope,omitempty"`
	Tools      []McpToolInfo             `json:"tools,omitempty"`
}

type McpStatusResponse struct {
	MCPServers []McpServerStatus `json:"mcpServers"`
}

type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred bool   `json:"isDeferred,omitempty"`
}

type ContextUsageResponse struct {
	Categories           []ContextUsageCategory `json:"categories"`
	TotalTokens          int                    `json:"totalTokens"`
	MaxTokens            int                    `json:"maxTokens"`
	RawMaxTokens         int                    `json:"rawMaxTokens"`
	Percentage           float64                `json:"percentage"`
	Model                string                 `json:"model"`
	IsAutoCompactEnabled bool                   `json:"isAutoCompactEnabled"`
	MemoryFiles          []map[string]any       `json:"memoryFiles"`
	MCPTools             []map[string]any       `json:"mcpTools"`
	Agents               []map[string]any       `json:"agents"`
	GridRows             [][]map[string]any     `json:"gridRows"`
	AutoCompactThreshold *int                   `json:"autoCompactThreshold,omitempty"`
	DeferredBuiltinTools []map[string]any       `json:"deferredBuiltinTools,omitempty"`
	SystemTools          []map[string]any       `json:"systemTools,omitempty"`
	SystemPromptSections []map[string]any       `json:"systemPromptSections,omitempty"`
	SlashCommands        map[string]any         `json:"slashCommands,omitempty"`
	Skills               map[string]any         `json:"skills,omitempty"`
	MessageBreakdown     map[string]any         `json:"messageBreakdown,omitempty"`
	APIUsage             map[string]any         `json:"apiUsage,omitempty"`
}

type MessageOriginKind = string

const (
	OriginHuman            MessageOriginKind = "human"
	OriginChannel          MessageOriginKind = "channel"
	OriginPeer             MessageOriginKind = "peer"
	OriginTaskNotification MessageOriginKind = "task-notification"
	OriginCoordinator      MessageOriginKind = "coordinator"
	OriginUnclassified     MessageOriginKind = "unclassified"
	OriginObserver         MessageOriginKind = "observer"
	OriginAutoContinuation MessageOriginKind = "auto-continuation"
	OriginObserverActivity MessageOriginKind = "observer-activity"
)

type TaskNotificationOriginSubkind = string

const (
	TaskNotifSubkindScheduledTrigger TaskNotificationOriginSubkind = "scheduled-trigger"
	TaskNotifSubkindPeerSendMessage  TaskNotificationOriginSubkind = "peer-send-message"
)

type MessageOrigin struct {
	Kind            MessageOriginKind             `json:"kind"`
	Server          string                        `json:"server,omitempty"`
	From            string                        `json:"from,omitempty"`
	Name            string                        `json:"name,omitempty"`
	FromSession     string                        `json:"fromSession,omitempty"`
	SenderTaskID    string                        `json:"senderTaskId,omitempty"`
	Body            string                        `json:"body,omitempty"`
	VerifiedPeerPID int                           `json:"verifiedPeerPid,omitempty"`
	Subkind         TaskNotificationOriginSubkind `json:"subkind,omitempty"`
}

type RateLimitStatus = string

const (
	RateLimitAllowed        RateLimitStatus = "allowed"
	RateLimitAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitRejected       RateLimitStatus = "rejected"
)

type RateLimitType = string

const (
	RateLimitFiveHour       RateLimitType = "five_hour"
	RateLimitSevenDay       RateLimitType = "seven_day"
	RateLimitSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitOverage        RateLimitType = "overage"
)

type TaskNotificationStatus = string

const (
	TaskNotifCompleted TaskNotificationStatus = "completed"
	TaskNotifFailed    TaskNotificationStatus = "failed"
	TaskNotifStopped   TaskNotificationStatus = "stopped"
)

type TaskUpdatedStatus = string

const (
	TaskUpdatedPending   TaskUpdatedStatus = "pending"
	TaskUpdatedRunning   TaskUpdatedStatus = "running"
	TaskUpdatedPaused    TaskUpdatedStatus = "paused"
	TaskUpdatedCompleted TaskUpdatedStatus = "completed"
	TaskUpdatedFailed    TaskUpdatedStatus = "failed"
	TaskUpdatedKilled    TaskUpdatedStatus = "killed"
)

var TerminalTaskStatuses = map[TaskUpdatedStatus]bool{
	TaskUpdatedCompleted: true,
	TaskUpdatedFailed:    true,
	TaskNotifStopped:     true,
	TaskUpdatedKilled:    true,
}

type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMS  int `json:"duration_ms"`
}

type SessionStoreFlushMode = string

const (
	FlushBatched SessionStoreFlushMode = "batched"
	FlushEager   SessionStoreFlushMode = "eager"
)

type SessionStoreSummaryEntry struct {
	SessionID string         `json:"session_id"`
	MTime     int64          `json:"mtime"`
	Data      map[string]any `json:"data"`
}

type AssistantMessageError = string

const (
	AsstErrAuthFailed   AssistantMessageError = "authentication_failed"
	AsstErrBillingError AssistantMessageError = "billing_error"
	AsstErrRateLimit    AssistantMessageError = "rate_limit"
	AsstErrInvalidReq   AssistantMessageError = "invalid_request"
	AsstErrServerError  AssistantMessageError = "server_error"
	AsstErrUnknown      AssistantMessageError = "unknown"
)

type ServerToolName = string

const (
	ServerToolAdvisor                 ServerToolName = "advisor"
	ServerToolWebSearch               ServerToolName = "web_search"
	ServerToolWebFetch                ServerToolName = "web_fetch"
	ServerToolCodeExecution           ServerToolName = "code_execution"
	ServerToolBashCodeExecution       ServerToolName = "bash_code_execution"
	ServerToolTextEditorCodeExecution ServerToolName = "text_editor_code_execution"
	ServerToolSearchToolRegex         ServerToolName = "tool_search_tool_regex"
	ServerToolSearchToolBM25          ServerToolName = "tool_search_tool_bm25"
)

type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
	CanonicalModel           string  `json:"canonicalModel,omitempty"`
	Provider                 string  `json:"provider,omitempty"`
}

func rawMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func rawSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func jsonCloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
