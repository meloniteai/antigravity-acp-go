package antigravityacp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type StepRow struct {
	Idx         int64
	StepType    int64
	Status      int64
	StepPayload *StepPayload
	Error       *ErrorDetails
	Permission  *PermissionInfo
	Task        *TaskDetails
}

type SessionUpdate struct {
	SessionUpdate     string           `json:"sessionUpdate"`
	MessageID         string           `json:"messageId,omitempty"`
	Content           interface{}      `json:"content,omitempty"`
	Title             string           `json:"title,omitempty"`
	ToolCallID        string           `json:"toolCallId,omitempty"`
	Kind              string           `json:"kind,omitempty"`
	Status            string           `json:"status,omitempty"`
	Locations         []interface{}    `json:"locations,omitempty"`
	RawInput          interface{}      `json:"rawInput,omitempty"`
	RawOutput         interface{}      `json:"rawOutput,omitempty"`
	AvailableCommands []Command        `json:"availableCommands,omitempty"`
	ConfigOptions     []ConfigOption   `json:"configOptions,omitempty"`
}

type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ConfigOption struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Category     string        `json:"category"`
	Type         string        `json:"type"`
	CurrentValue string        `json:"currentValue"`
	Options      []OptionValue `json:"options"`
}

type OptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func ParseRawInput(stepRow *StepRow) map[string]interface{} {
	if stepRow.StepPayload == nil || stepRow.StepPayload.ToolRun == nil || stepRow.StepPayload.ToolRun.Call == nil {
		return nil
	}
	rawJSON := stepRow.StepPayload.ToolRun.Call.RawInputJSON
	if strings.TrimSpace(rawJSON) == "" {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		return nil
	}
	return out
}

func Pick(o interface{}, keys ...string) interface{} {
	m, ok := o.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}
	for _, k := range keys {
		if val, exists := m[k]; exists {
			return val
		}
	}
	return nil
}

func AsStr(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func AsNum(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	case int:
		return int64(val)
	case string:
		var n int64
		if _, err := fmt.Sscan(val, &n); err == nil {
			return n
		}
	}
	return 0
}

func FsPath(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "file://") {
		u, err := url.Parse(p)
		if err == nil {
			return u.Path
		}
		return strings.TrimPrefix(p, "file://")
	}
	return p
}

func FencedCodeBlock(text string) string {
	fenceLen := 3
	run := 0
	for _, ch := range text {
		if ch == '`' {
			run++
		} else {
			run = 0
		}
		if run+1 > fenceLen {
			fenceLen = run + 1
		}
	}
	fence := strings.Repeat("`", fenceLen)
	return fence + "\n" + text + "\n" + fence
}

func ToDisplayPath(filePath string, cwd string) string {
	if cwd == "" {
		return filePath
	}
	resolvedCwd, err1 := filepath.Abs(cwd)
	resolvedFile, err2 := filepath.Abs(filePath)
	if err1 != nil || err2 != nil {
		return filePath
	}
	if strings.HasPrefix(resolvedFile, resolvedCwd+string(filepath.Separator)) || resolvedFile == resolvedCwd {
		rel, err := filepath.Rel(resolvedCwd, resolvedFile)
		if err == nil {
			return rel
		}
	}
	return filePath
}

func ToolKind(name string) string {
	l := strings.ToLower(name)
	if strings.Contains(l, "write") || strings.Contains(l, "edit") || strings.Contains(l, "patch") || strings.Contains(l, "replace") {
		return "edit"
	}
	if strings.Contains(l, "delete") || strings.Contains(l, "remove") {
		return "delete"
	}
	if strings.Contains(l, "move") || strings.Contains(l, "rename") {
		return "move"
	}
	if strings.Contains(l, "read") || strings.Contains(l, "view") || strings.Contains(l, "list") {
		return "read"
	}
	if strings.Contains(l, "grep") || strings.Contains(l, "search") || strings.Contains(l, "find") {
		return "search"
	}
	if strings.Contains(l, "command") || strings.Contains(l, "execute") || strings.Contains(l, "terminal") {
		return "execute"
	}
	if strings.Contains(l, "think") || strings.Contains(l, "thought") || strings.Contains(l, "reason") || strings.Contains(l, "plan") {
		return "think"
	}
	if strings.Contains(l, "url") || strings.Contains(l, "fetch") {
		return "fetch"
	}
	return "other"
}

func ToolCallID(stepRow *StepRow) string {
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil && stepRow.StepPayload.ToolRun.Call != nil && stepRow.StepPayload.ToolRun.Call.CallID != "" {
		return stepRow.StepPayload.ToolRun.Call.CallID
	}
	return fmt.Sprintf("agy-%d-%d", stepRow.Idx, stepRow.StepType)
}

func ToolCallStatus(stepRow *StepRow) string {
	switch stepRow.Status {
	case 2:
		return "in_progress"
	case 6, 7:
		return "failed"
	default:
		return "completed"
	}
}

func TextBlock(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "content",
		"content": map[string]interface{}{
			"type": "text",
			"text": text,
		},
	}
}

func CodeBlock(text string) map[string]interface{} {
	return TextBlock(FencedCodeBlock(text))
}

func TaskBlock(t *TaskDetails) map[string]interface{} {
	var lines []string
	if t.Description != "" {
		lines = append(lines, t.Description)
	}
	if t.TaskID != "" {
		lines = append(lines, fmt.Sprintf("Task: %s", t.TaskID))
	}
	if t.LogURI != "" {
		lines = append(lines, fmt.Sprintf("Log: %s", t.LogURI))
	}
	return TextBlock(strings.Join(lines, "\n"))
}

func PermissionBlock(p *PermissionInfo) map[string]interface{} {
	target := ""
	if strings.TrimSpace(p.Value) != "" {
		target = " (" + strings.TrimSpace(p.Value) + ")"
	}
	kind := p.Kind
	if kind == "" {
		kind = "unknown"
	}
	return TextBlock(fmt.Sprintf("Permission requested: %s%s", kind, target))
}

func ErrorBlock(e *ErrorDetails) map[string]interface{} {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = strings.TrimSpace(e.Detail)
	}
	if msg == "" {
		msg = "Tool call failed"
	}
	detail := ""
	trimmedDetail := strings.TrimSpace(e.Detail)
	if trimmedDetail != "" && trimmedDetail != msg {
		detail = "\n" + trimmedDetail
	}
	return CodeBlock(fmt.Sprintf("Error: %s%s", msg, detail))
}

func ToolCallUpdate(stepRow *StepRow, title string, kind string, status string, content []interface{}, locations []interface{}, rawInput interface{}) *SessionUpdate {
	if status == "" {
		status = ToolCallStatus(stepRow)
	}

	blocks := append([]interface{}{}, content...)
	if stepRow.Task != nil {
		blocks = append(blocks, TaskBlock(stepRow.Task))
	}
	if stepRow.Permission != nil {
		blocks = append(blocks, PermissionBlock(stepRow.Permission))
	}
	if stepRow.Error != nil {
		blocks = append(blocks, ErrorBlock(stepRow.Error))
	}

	var rawOutput interface{}
	if stepRow.Error != nil {
		msg := strings.TrimSpace(stepRow.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(stepRow.Error.Detail)
		}
		rawOutput = map[string]string{
			"message":    msg,
			"detail":     stepRow.Error.Detail,
			"stackTrace": stepRow.Error.StackTrace,
		}
	}

	update := &SessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    ToolCallID(stepRow),
		Title:         title,
		Kind:          kind,
		Status:        status,
		Content:       blocks,
		Locations:     locations,
		RawInput:      rawInput,
		RawOutput:     rawOutput,
	}
	return update
}
