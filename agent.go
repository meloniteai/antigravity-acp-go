package antigravityacp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentInfo         AgentInfo         `json:"agentInfo"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type AgentCapabilities struct {
	LoadSession        bool               `json:"loadSession"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
	Auth               AuthCapabilities   `json:"auth"`
}

type PromptCapabilities struct {
	EmbeddedContext bool `json:"embeddedContext"`
}

type SessionCapabilities struct {
	List                  struct{} `json:"list"`
	Delete                struct{} `json:"delete"`
	Resume                struct{} `json:"resume"`
	Close                 struct{} `json:"close"`
	AdditionalDirectories struct{} `json:"additionalDirectories"`
}

type AuthCapabilities struct {
	Logout struct{} `json:"logout"`
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CacheEntry struct {
	Updates       []*SessionUpdate
	MaxIdx        int64
	Stat          DbStat
	SkipNarration bool
	CWD           string
}

type ReplayCache struct {
	mu    sync.Mutex
	cache map[string]*CacheEntry
}

func NewReplayCache() *ReplayCache {
	return &ReplayCache{cache: make(map[string]*CacheEntry)}
}

func (c *ReplayCache) Get(dir, id string, skipNarration bool, cwd string) ([]*SessionUpdate, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stat, err := StatConversation(dir, id)
	if err != nil {
		return nil, -1, err
	}

	entry, exists := c.cache[id]
	if exists && entry.SkipNarration == skipNarration && entry.CWD == cwd {
		if entry.Stat.MtimeMs == stat.MtimeMs && entry.Stat.Size == stat.Size {
			return entry.Updates, entry.MaxIdx, nil
		}

		if stat.Size >= entry.Stat.Size {
			rows, err := ReadRows(dir, id, entry.MaxIdx)
			if err == nil {
				if len(rows) == 0 {
					entry.Stat = *stat
					return entry.Updates, entry.MaxIdx, nil
				}
				translator := NewTranslator(TranslatorOptions{
					Mode:          ModeReplay,
					SkipNarration: skipNarration,
					CWD:           cwd,
				})
				tail := translator.Translate(rows)
				newMaxIdx := entry.MaxIdx
				if translator.LastStepIdx() > newMaxIdx {
					newMaxIdx = translator.LastStepIdx()
				}
				newUpdates := append([]*SessionUpdate{}, entry.Updates...)
				newUpdates = append(newUpdates, tail...)

				entry.Updates = newUpdates
				entry.MaxIdx = newMaxIdx
				entry.Stat = *stat
				return newUpdates, newMaxIdx, nil
			}
		}
	}

	rows, err := ReadRows(dir, id, -1)
	if err != nil {
		return nil, -1, err
	}
	translator := NewTranslator(TranslatorOptions{
		Mode:          ModeReplay,
		SkipNarration: skipNarration,
		CWD:           cwd,
	})
	updates := translator.Translate(rows)
	entry = &CacheEntry{
		Updates:       updates,
		MaxIdx:        translator.LastStepIdx(),
		Stat:          *stat,
		SkipNarration: skipNarration,
		CWD:           cwd,
	}
	c.cache[id] = entry
	return updates, entry.MaxIdx, nil
}

func DiscoverModels(binary string) ([]string, error) {
	cmd := exec.Command(binary, "models")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var models []string
	lines := strings.Split(stdout.String(), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			models = append(models, trimmed)
		}
	}
	return models, nil
}

type AgyAcpAgent struct {
	mu               sync.Mutex
	binary           string
	conversationsDir string
	workingDir       string
	skipNarration    bool
	version          string
	sessions         *SessionManager
	adapter          *Adapter
	replayCache      *ReplayCache
	availableModels  []string
	activeClients    map[string]Client
}

func NewAgyAcpAgent(binary, conversationsDir, workingDir string, skipNarration bool, version string, store *SessionStore) *AgyAcpAgent {
	agent := &AgyAcpAgent{
		binary:           binary,
		conversationsDir: conversationsDir,
		workingDir:       workingDir,
		skipNarration:    skipNarration,
		version:          version,
		sessions:         NewSessionManager(store, 64),
		adapter:          NewAdapter(binary, conversationsDir, workingDir, skipNarration),
		replayCache:      NewReplayCache(),
		activeClients:    make(map[string]Client),
	}

	stateDir := filepath.Join(os.Getenv("HOME"), ".agy-acp")
	cacheFile := filepath.Join(stateDir, "models.json")
	if data, err := os.ReadFile(cacheFile); err == nil {
		var cached []string
		if err := json.Unmarshal(data, &cached); err == nil && len(cached) > 0 {
			agent.availableModels = cached
		}
	}

	go func() {
		models, err := DiscoverModels(binary)
		if err == nil && len(models) > 0 {
			agent.mu.Lock()
			changed := false
			if len(models) != len(agent.availableModels) {
				changed = true
			} else {
				for i, m := range models {
					if m != agent.availableModels[i] {
						changed = true
						break
					}
				}
			}
			if changed {
				agent.availableModels = models
				agent.mu.Unlock()
				agent.pushConfigOptionUpdates()
				_ = os.MkdirAll(stateDir, 0755)
				if mData, err := json.Marshal(models); err == nil {
					_ = os.WriteFile(cacheFile, mData, 0644)
				}
			} else {
				agent.mu.Unlock()
			}
		}
	}()

	return agent
}

func (a *AgyAcpAgent) pushConfigOptionUpdates() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for sessionID, client := range a.activeClients {
		sess, err := a.sessions.Ensure(sessionID)
		if err == nil && sess != nil {
			opts := a.configOptions(sess)
			if len(opts) > 0 {
				_ = client.Update(sessionID, &SessionUpdate{
					SessionUpdate: "config_option_update",
					ConfigOptions: opts,
				})
			}
		}
	}
}

func (a *AgyAcpAgent) configOptions(session *Session) []ConfigOption {
	a.mu.Lock()
	models := a.availableModels
	a.mu.Unlock()

	var options []ConfigOption
	if len(models) > 0 {
		currentModel := session.ModelID
		if currentModel == "" {
			currentModel = models[0]
		}
		var modelOptions []OptionValue
		for _, m := range models {
			modelOptions = append(modelOptions, OptionValue{Value: m, Name: m})
		}
		options = append(options, ConfigOption{
			ID:           "model",
			Name:         "Model",
			Category:     "model",
			Type:         "select",
			CurrentValue: currentModel,
			Options:      modelOptions,
		})
	}

	pm := session.PermissionMode
	currentMode := "default"
	if pm == "bypassPermissions" {
		currentMode = "bypassPermissions"
	} else if pm == "plan" {
		currentMode = "plan"
	}

	options = append(options, ConfigOption{
		ID:           "mode",
		Name:         "Mode",
		Category:     "mode",
		Type:         "select",
		CurrentValue: currentMode,
		Options: []OptionValue{
			{Value: "default", Name: "Standard", Description: "Antigravity's standard mode"},
			{Value: "plan", Name: "Plan Mode", Description: "Read-only exploration: agent may only read and search, then returns a step-by-step plan without making any changes"},
			{Value: "bypassPermissions", Name: "Skip Permissions", Description: "Run without permission prompts — use with caution, as this may allow the agent to make changes without confirmation"},
		},
	})

	return options
}

func (a *AgyAcpAgent) Initialize() *InitializeResponse {
	return &InitializeResponse{
		ProtocolVersion: 1,
		AgentInfo:       AgentInfo{Name: "Antigravity", Version: a.version},
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: PromptCapabilities{
				EmbeddedContext: true,
			},
			SessionCapabilities: SessionCapabilities{
				List:                  struct{}{},
				Delete:                struct{}{},
				Resume:                struct{}{},
				Close:                 struct{}{},
				AdditionalDirectories: struct{}{},
			},
			Auth: AuthCapabilities{
				Logout: struct{}{},
			},
		},
		AuthMethods: []AuthMethod{
			{
				ID:          "agy-agent",
				Name:        "Google Sign In",
				Description: "Antigravity uses Google OAuth2 credentials managed by the agy CLI. Run `agy` to configure authentication if needed.",
			},
		},
	}
}

func (a *AgyAcpAgent) Authenticate(methodID string) error {
	if methodID != "" && methodID != "agy-agent" {
		return errors.New("unknown auth method: " + methodID)
	}
	return nil
}

func (a *AgyAcpAgent) Logout() {}

func (a *AgyAcpAgent) NewSession(cwd string, additionalDirs []string, client Client) (string, []ConfigOption) {
	if cwd == "" {
		cwd = a.workingDir
	}
	sessionID, session := a.sessions.Create(cwd, additionalDirs)
	a.mu.Lock()
	a.activeClients[sessionID] = client
	a.mu.Unlock()

	a.announceSession(client, sessionID, session)
	return sessionID, a.configOptions(session)
}

func (a *AgyAcpAgent) LoadSession(sessionID string, cwd string, additionalDirs []string, client Client) ([]ConfigOption, error) {
	session, err := a.sessions.Ensure(sessionID)
	if err != nil || session == nil {
		return nil, errors.New("session not found: " + sessionID)
	}

	if cwd != "" {
		session.CWD = cwd
	}
	if len(additionalDirs) > 0 {
		session.AdditionalDirs = additionalDirs
	}

	if session.ConversationID != "" {
		updates, maxIdx, err := a.replayCache.Get(a.conversationsDir, session.ConversationID, a.skipNarration, session.CWD)
		if err == nil && len(updates) > 0 {
			for _, update := range updates {
				_ = client.Update(sessionID, update)
			}
			session.LastStepIdx = maxIdx
			_ = a.sessions.Persist(sessionID, session)
		}
	}

	a.mu.Lock()
	a.activeClients[sessionID] = client
	a.mu.Unlock()

	a.announceSession(client, sessionID, session)
	return a.configOptions(session), nil
}

func (a *AgyAcpAgent) ResumeSession(sessionID string, cwd string, additionalDirs []string, client Client) ([]ConfigOption, error) {
	session, err := a.sessions.Ensure(sessionID)
	if err != nil || session == nil {
		return nil, errors.New("session not found: " + sessionID)
	}

	dirty := false
	if cwd != "" && session.CWD != cwd {
		session.CWD = cwd
		dirty = true
	}
	if len(additionalDirs) > 0 {
		session.AdditionalDirs = additionalDirs
		dirty = true
	}
	if dirty {
		_ = a.sessions.Persist(sessionID, session)
	}

	a.mu.Lock()
	a.activeClients[sessionID] = client
	a.mu.Unlock()

	a.announceSession(client, sessionID, session)
	return a.configOptions(session), nil
}

type SessionInfo struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

func (a *AgyAcpAgent) ListSessions(cwd string) ([]SessionInfo, error) {
	all, err := a.sessions.List()
	if err != nil {
		return nil, err
	}
	var res []SessionInfo
	for _, entry := range all {
		if cwd != "" && entry.Session.CWD != cwd {
			continue
		}
		res = append(res, SessionInfo{
			SessionID: entry.SessionID,
			CWD:       entry.Session.CWD,
			Title:     entry.Session.Title,
			UpdatedAt: entry.Session.UpdatedAt,
		})
	}
	return res, nil
}

func (a *AgyAcpAgent) DeleteSession(sessionID string) (bool, error) {
	deleted, err := a.sessions.Delete(sessionID)
	a.mu.Lock()
	delete(a.activeClients, sessionID)
	a.mu.Unlock()
	return deleted, err
}

func (a *AgyAcpAgent) CloseSession(sessionID string) {
	a.adapter.Cancel(sessionID)
	a.sessions.Evict(sessionID)
	a.mu.Lock()
	delete(a.activeClients, sessionID)
	a.mu.Unlock()
}

func (a *AgyAcpAgent) Prompt(sessionID string, prompt interface{}, client Client) (*PromptOutcome, error) {
	session, err := a.sessions.Ensure(sessionID)
	if err != nil || session == nil {
		_, session = a.sessions.Create(a.workingDir, nil)
		a.sessions.Adopt(sessionID, session)
	}

	rawText := PromptText(prompt)
	text := rawText
	if session.PermissionMode == "plan" {
		planModeInjection := "<system>\n[PLANNING MODE] You must NOT write, edit, create, move, or execute any files or " +
			"commands. You may only read, search, and explore. Your response must be a clear.\n" +
			"Exception: You can run commands and fetch data from the web which lets you explore, read, search and collect " +
			"more information for creating a plan. " +
			"You can use a tool to write a step - by - step implementation plan for how to accomplish the following task " +
			"— strictly do not start implementing it:\n<system>\n"
		text = planModeInjection + rawText
	}

	outcome, err := a.adapter.RunPrompt(sessionID, session, text, client)
	if err != nil {
		return nil, err
	}
	if outcome.Error != "" {
		return outcome, nil
	}

	if session.ConversationID == "" {
		session.ConversationID = outcome.ConversationID
	}
	if outcome.ConversationID != "" {
		session.LastStepIdx = outcome.LastStepIdx
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = a.sessions.Persist(sessionID, session)
	}

	return outcome, nil
}

func (a *AgyAcpAgent) Cancel(sessionID string) {
	a.adapter.Cancel(sessionID)
}

func (a *AgyAcpAgent) SetConfigOption(sessionID string, configID string, value string) ([]ConfigOption, error) {
	if configID != "model" && configID != "mode" {
		return nil, errors.New("unknown configId: " + configID)
	}
	if value == "" {
		return nil, errors.New("missing value")
	}
	session, err := a.sessions.Ensure(sessionID)
	if err != nil || session == nil {
		return nil, errors.New("session not found: " + sessionID)
	}

	if configID == "model" {
		session.ModelID = value
	} else if configID == "mode" {
		session.PermissionMode = value
	}
	_ = a.sessions.Persist(sessionID, session)
	return a.configOptions(session), nil
}

func (a *AgyAcpAgent) announceSession(client Client, sessionID string, session *Session) {
	go func() {
		time.Sleep(50 * time.Millisecond)
		availableCommands := []Command{
			{Name: "goal", Description: "Run a long-running task thoroughly"},
			{Name: "schedule", Description: "Run an instruction on a recurring schedule or set a timer"},
			{Name: "grill-me", Description: "Align on a plan through an interactive interview"},
			{Name: "teamwork-preview", Description: "Preview a team of autonomous agents working together"},
			{Name: "learn", Description: "Persist a behavior for future tasks"},
		}
		_ = client.Update(sessionID, &SessionUpdate{
			SessionUpdate:     "available_commands_update",
			AvailableCommands: availableCommands,
		})

		opts := a.configOptions(session)
		if len(opts) > 0 {
			_ = client.Update(sessionID, &SessionUpdate{
				SessionUpdate: "config_option_update",
				ConfigOptions: opts,
			})
		}
	}()
}

func PromptText(prompt interface{}) string {
	if s, ok := prompt.(string); ok {
		return s
	}
	blocks, ok := prompt.([]interface{})
	if !ok {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		obj, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		typ := AsStr(obj["type"])
		if typ == "text" {
			text := AsStr(obj["text"])
			parts = append(parts, fmt.Sprintf("<user_text>\n%s\n</user_text>", text))
		} else if typ == "resource_link" {
			uri := AsStr(obj["uri"])
			title := AsStr(obj["title"])
			if title == "" {
				title = AsStr(obj["name"])
			}
			if title == "" {
				title = uri
			}
			parts = append(parts, fmt.Sprintf(`<resource_link uri="%s" title="%s"/>`, escapeAttr(uri), escapeAttr(title)))
		} else if typ == "resource" {
			resObj, _ := obj["resource"].(map[string]interface{})
			if resObj != nil {
				uri := AsStr(resObj["uri"])
				text := AsStr(resObj["text"])
				if text == "" {
					text = AsStr(resObj["content"])
				}
				parts = append(parts, fmt.Sprintf("<embedded_resource uri=\"%s\">\n%s\n</embedded_resource>", escapeAttr(uri), text))
			}
		} else if txt := AsStr(obj["text"]); txt != "" {
			parts = append(parts, fmt.Sprintf("<user_text>\n%s\n</user_text>", txt))
		}
	}
	return strings.Join(parts, "\n\n")
}

func escapeAttr(s string) string {
	return strings.ReplaceAll(s, "\"", "&quot;")
}
