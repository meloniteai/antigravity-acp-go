package antigravityacp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type TranslateMode string

const (
	ModeStream TranslateMode = "stream"
	ModeReplay TranslateMode = "replay"
)

type TranslatorOptions struct {
	Mode          TranslateMode
	SkipNarration bool
	CWD           string
}

type Translator struct {
	opts              TranslatorOptions
	agentTextLengths  map[int64]int
	emittedSteps      map[int64]bool
	pendingAgentParts []string
	lastTitle         string
	lastStepIdx       int64
	hadUpdates        bool
}

func NewTranslator(opts TranslatorOptions) *Translator {
	return &Translator{
		opts:             opts,
		agentTextLengths: make(map[int64]int),
		emittedSteps:     make(map[int64]bool),
		lastStepIdx:      -1,
	}
}

func (t *Translator) LastStepIdx() int64 {
	return t.lastStepIdx
}

func (t *Translator) HadUpdates() bool {
	return t.hadUpdates
}

func (t *Translator) Translate(rows []*StepRow) []*SessionUpdate {
	var out []*SessionUpdate
	for _, row := range rows {
		t.translateRow(row, &out)
	}
	if t.opts.Mode == ModeReplay {
		t.flushAgentBuffer(&out)
	}
	if len(out) > 0 {
		t.hadUpdates = true
	}
	return out
}

func (t *Translator) translateRow(row *StepRow, out *[]*SessionUpdate) {
	if row.Idx > t.lastStepIdx {
		t.lastStepIdx = row.Idx
	}

	switch row.StepType {
	case 15:
		t.handleAgentText(row, out)
	case 23:
		t.handleTitle(row, out)
	case 14:
		if t.opts.Mode == ModeStream {
			return
		}
		t.flushAgentBuffer(out)
		t.pushDispatched(row, out)
	default:
		if t.opts.Mode == ModeReplay {
			t.flushAgentBuffer(out)
		} else if t.emittedSteps[row.Idx] {
			return
		}
		t.emittedSteps[row.Idx] = true
		t.pushDispatched(row, out)
	}
}

func (t *Translator) pushDispatched(row *StepRow, out *[]*SessionUpdate) {
	updates := BuildUpdateFromStepPayload(row, t.opts.CWD)
	*out = append(*out, updates...)
}

func (t *Translator) handleTitle(row *StepRow, out *[]*SessionUpdate) {
	title := ""
	if row.StepPayload != nil && row.StepPayload.TitleUpdate != nil {
		title = row.StepPayload.TitleUpdate.Title
	}
	blocks := strings.Split(title, "\n\n")
	currentTitle := ""
	if len(blocks) > 0 {
		currentTitle = blocks[0]
		blocks = blocks[1:]
	}

	if currentTitle != t.lastTitle {
		t.lastTitle = currentTitle
		*out = append(*out, &SessionUpdate{
			SessionUpdate: "session_info_update",
			Title:         currentTitle,
		})
	}

	var nonBgBlocks []string
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			nonBgBlocks = append(nonBgBlocks, b)
		}
	}
	if len(nonBgBlocks) == 0 {
		return
	}

	*out = append(*out, &SessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    ToolCallID(row),
		Title:         "Think",
		Kind:          "think",
		Status:        "completed",
		Content: []interface{}{
			map[string]interface{}{
				"type": "content",
				"content": map[string]interface{}{
					"type": "text",
					"text": strings.Join(nonBgBlocks, "\n\n"),
				},
			},
		},
	})
}

func (t *Translator) handleAgentText(row *StepRow, out *[]*SessionUpdate) {
	text := ""
	if row.StepPayload != nil && row.StepPayload.AgentText != nil {
		text = row.StepPayload.AgentText.Text
	}

	if t.opts.Mode == ModeReplay {
		if len(text) > 0 {
			t.pendingAgentParts = append(t.pendingAgentParts, text)
		}
		return
	}

	emitted := t.agentTextLengths[row.Idx]
	if len(text) <= emitted {
		return
	}
	t.agentTextLengths[row.Idx] = len(text)
	if t.opts.SkipNarration && IsNarration(text) {
		return
	}
	delta := text[emitted:]
	if len(delta) > 0 {
		*out = append(*out, &SessionUpdate{
			SessionUpdate: "agent_message_chunk",
			Content: map[string]interface{}{
				"type": "text",
				"text": delta,
			},
			MessageID: fmt.Sprintf("%d", row.Idx),
		})
	}
}

func (t *Translator) flushAgentBuffer(out *[]*SessionUpdate) {
	if len(t.pendingAgentParts) == 0 {
		return
	}
	var text string
	if t.opts.SkipNarration {
		text = FilterNarration(t.pendingAgentParts)
	} else {
		text = strings.Join(t.pendingAgentParts, "\n")
	}
	t.pendingAgentParts = nil
	if len(text) > 0 {
		*out = append(*out, &SessionUpdate{
			SessionUpdate: "agent_message_chunk",
			Content: map[string]interface{}{
				"type": "text",
				"text": text,
			},
		})
	}
}

var promptRegex = regexp.MustCompile(`<user_text>\n([\s\S]*?)\n<\/user_text>|<resource_link uri="(.*?)" title="(.*?)"\/>|<embedded_resource uri="(.*?)">\n([\s\S]*?)\n<\/embedded_resource>`)
var systemRegex = regexp.MustCompile(`(?s)^<system>\n\[PLANNING MODE\][\s\S]*?\n<\/?system>\n?`)

func UserPromptUpdate(stepRow *StepRow) []*SessionUpdate {
	var text string
	if stepRow.StepPayload != nil && stepRow.StepPayload.UserPrompt != nil {
		text = stepRow.StepPayload.UserPrompt.Text
		if text == "" && stepRow.StepPayload.UserPrompt.Content != nil {
			text = stepRow.StepPayload.UserPrompt.Content.Text
		}
	}
	text = strings.TrimSpace(text)
	text = systemRegex.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)

	var blocks []interface{}
	matches := promptRegex.FindAllStringSubmatch(text, -1)
	foundAny := len(matches) > 0

	for _, match := range matches {
		if match[1] != "" {
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": match[1],
			})
		} else if match[2] != "" {
			uri := strings.ReplaceAll(match[2], "&quot;", "\"")
			title := strings.ReplaceAll(match[3], "&quot;", "\"")
			blocks = append(blocks, map[string]interface{}{
				"type": "resource_link",
				"uri":  uri,
				"name": title,
				"title": title,
			})
		} else if match[4] != "" {
			uri := strings.ReplaceAll(match[4], "&quot;", "\"")
			textContent := match[5]
			blocks = append(blocks, map[string]interface{}{
				"type": "resource",
				"resource": map[string]interface{}{
					"uri":  uri,
					"text": textContent,
				},
			})
		}
	}

	if !foundAny {
		blocks = append(blocks, map[string]interface{}{
			"type": "text",
			"text": text,
		})
	}

	var updates []*SessionUpdate
	for _, content := range blocks {
		updates = append(updates, &SessionUpdate{
			SessionUpdate: "user_message_chunk",
			Content:       content,
			MessageID:     fmt.Sprintf("%d", stepRow.Idx),
		})
	}
	return updates
}

func AgentUpdate(stepRow *StepRow) *SessionUpdate {
	text := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.AgentText != nil {
		text = stepRow.StepPayload.AgentText.Text
	}
	return &SessionUpdate{
		SessionUpdate: "agent_message_chunk",
		Content: map[string]interface{}{
			"type": "text",
			"text": text,
		},
		MessageID: fmt.Sprintf("%d", stepRow.Idx),
	}
}

func TitleUpdateFunc(stepRow *StepRow) []*SessionUpdate {
	title := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.TitleUpdate != nil {
		title = stepRow.StepPayload.TitleUpdate.Title
	}
	var updates []*SessionUpdate
	blocks := strings.Split(title, "\n\n")
	currentTitle := ""
	if len(blocks) > 0 {
		currentTitle = blocks[0]
		blocks = blocks[1:]
	}
	updates = append(updates, &SessionUpdate{
		SessionUpdate: "session_info_update",
		Title:         currentTitle,
	})

	var nonBgBlocks []string
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			nonBgBlocks = append(nonBgBlocks, b)
		}
	}
	if len(nonBgBlocks) == 0 {
		return updates
	}

	updates = append(updates, &SessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    ToolCallID(stepRow),
		Title:         "Think",
		Kind:          "think",
		Status:        "completed",
		Content: []interface{}{
			map[string]interface{}{
				"type": "content",
				"content": map[string]interface{}{
					"type": "text",
					"text": strings.Join(nonBgBlocks, "\n\n"),
				},
			},
		},
	})
	return updates
}

func isPlanFile(targetFile string) bool {
	return strings.Contains(targetFile, ".gemini") &&
		strings.Contains(targetFile, "antigravity-cli") &&
		strings.Contains(targetFile, "brain") &&
		strings.HasSuffix(targetFile, ".md")
}

func EditUpdate(stepRow *StepRow, cwd string) []*SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	targetFile := FsPath(AsStr(Pick(rawInput, "TargetFile", "targetFile")))
	shown := ""
	if targetFile != "" {
		shown = ToDisplayPath(targetFile, cwd)
	}

	title := "Edit"
	if isPlanFile(targetFile) {
		title = filepath.Base(shown)
		if title == "." || title == "" {
			title = "Implementation Plan"
		}
	} else if shown != "" {
		title = "Edit " + shown
	}

	var content []interface{}
	var locations []interface{}

	codeContentVal := Pick(rawInput, "CodeContent", "codeContent")
	if codeContentVal != nil {
		fullContent := AsStr(codeContentVal)
		if isPlanFile(targetFile) {
			content = append(content, TextBlock(fullContent))
		} else if targetFile != "" {
			content = append(content, map[string]interface{}{
				"type":    "diff",
				"path":    targetFile,
				"oldText": nil,
				"newText": fullContent,
			})
		}
		if targetFile != "" {
			locations = append(locations, map[string]interface{}{"path": targetFile})
		}
	} else {
		chunksRaw := Pick(rawInput, "ReplacementChunks", "replacementChunks")
		var chunks []interface{}
		if arr, ok := chunksRaw.([]interface{}); ok {
			chunks = arr
		} else if chunksRaw != nil {
			chunks = append(chunks, chunksRaw)
		} else if rawInput != nil {
			chunks = append(chunks, rawInput)
		}

		for _, chunk := range chunks {
			if isPlanFile(targetFile) {
				continue
			}
			oldText := AsStr(Pick(chunk, "TargetContent", "targetContent"))
			newTextVal := Pick(chunk, "ReplacementContent", "replacementContent")
			if newTextVal == nil {
				continue
			}
			newText := AsStr(newTextVal)

			if targetFile != "" {
				content = append(content, map[string]interface{}{
					"type":    "diff",
					"path":    targetFile,
					"oldText": oldText,
					"newText": newText,
				})

				lineVal := Pick(chunk, "StartLine", "startLine")
				loc := map[string]interface{}{"path": targetFile}
				if lineVal != nil {
					loc["line"] = AsNum(lineVal)
				}
				locations = append(locations, loc)
			}
		}
	}

	if isPlanFile(targetFile) && len(content) == 0 {
		return nil
	}

	return []*SessionUpdate{ToolCallUpdate(stepRow, title, "edit", "", content, locations, rawInput)}
}

func ReadUpdate(stepRow *StepRow, cwd string) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	name := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil && stepRow.StepPayload.ToolRun.Call != nil {
		name = stepRow.StepPayload.ToolRun.Call.NamePrimary
	}

	var view *ViewFileResult
	var list *ListDirectoryResult
	if stepRow.StepPayload != nil {
		view = stepRow.StepPayload.ViewFile
		list = stepRow.StepPayload.ListDirectory
	}

	title := "Read"
	var content []interface{}
	var locations []interface{}

	if list != nil || name == "list_dir" || stepRow.StepType == 9 {
		dirVal := Pick(rawInput, "DirectoryPath", "directoryPath")
		var dir string
		if dirVal != nil {
			dir = FsPath(AsStr(dirVal))
		} else if list != nil {
			dir = FsPath(list.DirURI)
		}
		shown := ""
		if dir != "" {
			shown = ToDisplayPath(dir, cwd)
		}
		if shown != "" {
			title = "Read " + shown
		} else {
			title = "Read directory"
		}
		if dir != "" {
			locations = append(locations, map[string]interface{}{"path": dir})
		}

		var entries []DirEntry
		if list != nil {
			entries = list.Entries
		}
		var entryNames []string
		for _, e := range entries {
			if strings.TrimSpace(e.Name) != "" {
				suffix := ""
				if e.IsDirectory != 0 {
					suffix = "/"
				}
				entryNames = append(entryNames, e.Name+suffix)
			}
		}
		if len(entryNames) > 0 {
			content = append(content, CodeBlock(strings.Join(entryNames, "\n")))
		}
	} else {
		filePathVal := Pick(rawInput, "AbsolutePath", "absolutePath", "FilePath")
		var filePath string
		if filePathVal != nil {
			filePath = FsPath(AsStr(filePathVal))
		} else if view != nil {
			filePath = FsPath(view.FileURI)
		}
		shown := ""
		if filePath != "" {
			shown = ToDisplayPath(filePath, cwd)
		}

		startLine := int64(1)
		startLineVal := Pick(rawInput, "StartLine", "startLine")
		if startLineVal != nil {
			startLine = AsNum(startLineVal)
		} else if view != nil {
			startLine = view.StartLine
		}
		if startLine == 0 {
			startLine = 1
		}

		var endLine int64
		endLineVal := Pick(rawInput, "EndLine", "endLine")
		if endLineVal != nil {
			endLine = AsNum(endLineVal)
		} else if view != nil {
			endLine = view.EndLine
		}

		if shown != "" {
			title = "Read " + shown
			if endLine > 0 {
				title += fmt.Sprintf(":%d-%d", startLine, endLine)
			}
		} else {
			title = "Read file"
		}

		if filePath != "" {
			locations = append(locations, map[string]interface{}{"path": filePath, "line": startLine})
		}

		body := ""
		if view != nil {
			body = view.Content
		}
		if body != "" {
			content = append(content, CodeBlock(body))
		}
	}

	return ToolCallUpdate(stepRow, title, "read", "", content, locations, rawInput)
}

func renderHits(hits []SearchHit) string {
	var lines []string
	for _, h := range hits {
		var parts []string
		for _, field := range []string{h.Field1, h.Field2, h.Field3, h.Field4, h.Field5} {
			if strings.TrimSpace(field) != "" {
				parts = append(parts, field)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, " | "))
		}
	}
	return strings.Join(lines, "\n")
}

func SearchUpdate(stepRow *StepRow, cwd string) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	var grep *GrepSearchResult
	if stepRow.StepPayload != nil {
		grep = stepRow.StepPayload.GrepSearch
	}

	name := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil && stepRow.StepPayload.ToolRun.Call != nil {
		name = stepRow.StepPayload.ToolRun.Call.NamePrimary
	}

	title := "Search"
	var content []interface{}
	var locations []interface{}

	if grep != nil || name == "grep_search" || stepRow.StepType == 7 {
		query := ""
		if grep != nil {
			query = grep.Query
		} else {
			query = AsStr(Pick(rawInput, "Query", "query"))
		}

		searchPathVal := Pick(rawInput, "SearchPath", "searchPath")
		var searchPath string
		if searchPathVal != nil {
			searchPath = FsPath(AsStr(searchPathVal))
		} else if grep != nil {
			searchPath = FsPath(grep.CwdURI)
		}

		shown := ""
		if searchPath != "" {
			shown = ToDisplayPath(searchPath, cwd)
		}

		if shown != "" {
			title = fmt.Sprintf("Search '%s' %s", query, shown)
		} else {
			title = fmt.Sprintf("Search '%s'", query)
		}

		if searchPath != "" {
			locations = append(locations, map[string]interface{}{"path": searchPath})
		}

		body := ""
		if grep != nil {
			body = grep.TextOutput
			if body == "" {
				body = renderHits(grep.Hits)
			}
			if body == "" {
				body = grep.ShellCommand
			}
		}
		if body != "" {
			content = append(content, CodeBlock(body))
		}
	} else {
		query := strings.TrimSpace(AsStr(Pick(rawInput, "query", "Query")))
		if query != "" {
			title = "Web search " + query
		} else {
			title = "Web search"
		}
	}

	return ToolCallUpdate(stepRow, title, "search", "", content, locations, rawInput)
}

func ExecuteUpdate(stepRow *StepRow, _cwd string) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	cmd := AsStr(Pick(rawInput, "CommandLine", "commandLine", "command"))
	firstLine := ""
	if cmd != "" {
		firstLine = strings.TrimSpace(strings.Split(cmd, "\n")[0])
	}

	title := firstLine
	if title == "" {
		if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
			title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitlePrimary)
			if title == "" {
				title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitleSecondary)
			}
		}
	}
	if title == "" {
		title = "Command Execution"
	}

	var content []interface{}
	if cmd != "" {
		content = append(content, CodeBlock(cmd))
	}

	var locations []interface{}
	cmdCwd := FsPath(AsStr(Pick(rawInput, "Cwd", "cwd")))
	if cmdCwd != "" {
		locations = append(locations, map[string]interface{}{"path": cmdCwd})
	}

	return ToolCallUpdate(stepRow, title, "execute", "", content, locations, rawInput)
}

func FetchUpdate(stepRow *StepRow) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	urlVal := AsStr(Pick(rawInput, "Url", "url"))

	title := "Fetch URL"
	if urlVal != "" {
		title = "Fetch " + urlVal
	} else if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
		title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitlePrimary)
		if title == "" {
			title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitleSecondary)
		}
	}
	if title == "" {
		title = "Fetch URL"
	}

	var content []interface{}
	if urlVal != "" {
		content = append(content, TextBlock(urlVal))
	}

	return ToolCallUpdate(stepRow, title, "fetch", "", content, nil, rawInput)
}

func SubagentUpdate(stepRow *StepRow) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	subagentsVal := Pick(rawInput, "Subagents", "subagents")
	var subagents []interface{}
	if arr, ok := subagentsVal.([]interface{}); ok {
		subagents = arr
	}

	title := "Invoke subagent"
	if len(subagents) > 0 {
		suffix := ""
		if len(subagents) > 1 {
			suffix = "s"
		}
		title = fmt.Sprintf("Delegate to %d subagent%s", len(subagents), suffix)
	} else if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
		title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitleSecondary)
		if title == "" {
			title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitlePrimary)
		}
	}
	if title == "" {
		title = "Invoke subagent"
	}

	var content []interface{}
	for _, sub := range subagents {
		prompt := strings.TrimSpace(AsStr(Pick(sub, "Prompt", "prompt")))
		if prompt != "" {
			content = append(content, CodeBlock(prompt))
		}
	}

	return ToolCallUpdate(stepRow, title, "other", "", content, nil, rawInput)
}

func QuestionUpdate(stepRow *StepRow) *SessionUpdate {
	rawInput := ParseRawInput(stepRow)
	questionsVal := Pick(rawInput, "questions", "Questions")
	var questions []interface{}
	if arr, ok := questionsVal.([]interface{}); ok {
		questions = arr
	}

	firstQuestion := ""
	if len(questions) > 0 {
		firstQuestion = strings.TrimSpace(AsStr(Pick(questions[0], "question", "Question")))
	}

	title := firstQuestion
	if title == "" && stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
		title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitlePrimary)
		if title == "" {
			title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitleSecondary)
		}
	}
	if title == "" {
		title = "Ask question"
	}

	var content []interface{}
	for _, q := range questions {
		question := strings.TrimSpace(AsStr(Pick(q, "question", "Question")))
		if question == "" {
			continue
		}
		optionsVal := Pick(q, "options", "Options")
		var options []interface{}
		if arr, ok := optionsVal.([]interface{}); ok {
			options = arr
		}
		lines := []string{question}
		for _, opt := range options {
			label := AsStr(opt)
			if label == "" {
				label = AsStr(Pick(opt, "label", "Label"))
			}
			if label != "" {
				lines = append(lines, "  - "+label)
			}
		}
		content = append(content, TextBlock(strings.Join(lines, "\n")))
	}

	return ToolCallUpdate(stepRow, title, "other", "", content, nil, rawInput)
}

func OtherUpdate(stepRow *StepRow) *SessionUpdate {
	name := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil && stepRow.StepPayload.ToolRun.Call != nil {
		name = stepRow.StepPayload.ToolRun.Call.NamePrimary
	}
	rawInput := ParseRawInput(stepRow)

	switch name {
	case "manage_task":
		action := strings.TrimSpace(AsStr(Pick(rawInput, "Action", "action")))
		if action == "" {
			action = "manage"
		}
		taskId := AsStr(Pick(rawInput, "TaskId", "taskId"))
		title := "Manage task " + action
		var content []interface{}
		if taskId != "" {
			content = append(content, TextBlock("Task: "+taskId))
		}
		return ToolCallUpdate(stepRow, title, "other", "", content, nil, rawInput)

	case "schedule":
		duration := AsStr(Pick(rawInput, "DurationSeconds", "durationSeconds"))
		prompt := strings.TrimSpace(AsStr(Pick(rawInput, "Prompt", "prompt")))
		title := "Schedule timer"
		if duration != "" {
			title = fmt.Sprintf("Schedule timer (%ss)", duration)
		}
		var content []interface{}
		if prompt != "" {
			content = append(content, TextBlock(prompt))
		}
		return ToolCallUpdate(stepRow, title, "other", "", content, nil, rawInput)

	case "send_message":
		message := strings.TrimSpace(AsStr(Pick(rawInput, "Message", "message")))
		title := "Send message to subagent"
		var content []interface{}
		if message != "" {
			content = append(content, TextBlock(message))
		}
		return ToolCallUpdate(stepRow, title, "other", "", content, nil, rawInput)

	case "manage_subagents":
		action := strings.TrimSpace(AsStr(Pick(rawInput, "Action", "action")))
		if action == "" {
			action = "manage"
		}
		return ToolCallUpdate(stepRow, "Subagents: "+action, "other", "", nil, nil, rawInput)
	}

	title := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
		title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitlePrimary)
	}
	if title == "" {
		title = strings.TrimSpace(AsStr(Pick(rawInput, "toolSummary", "ToolSummary")))
	}
	if title == "" && stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil {
		title = strings.TrimSpace(stepRow.StepPayload.ToolRun.TitleSecondary)
	}
	if title == "" {
		title = name
	}
	if title == "" {
		title = "Tool"
	}

	var content []interface{}
	if rawInput != nil {
		rest := make(map[string]interface{})
		for k, v := range rawInput {
			if k != "toolAction" && k != "toolSummary" {
				rest[k] = v
			}
		}
		if len(rest) > 0 {
			data, err := json.MarshalIndent(rest, "", "  ")
			if err == nil {
				content = append(content, CodeBlock(string(data)))
			}
		}
	}

	return ToolCallUpdate(stepRow, title, ToolKind(name), "", content, nil, rawInput)
}

func buildByToolName(stepRow *StepRow, cwd string) []*SessionUpdate {
	name := ""
	if stepRow.StepPayload != nil && stepRow.StepPayload.ToolRun != nil && stepRow.StepPayload.ToolRun.Call != nil {
		name = stepRow.StepPayload.ToolRun.Call.NamePrimary
	}
	if name == "" {
		return nil
	}
	if name == "view_file" || name == "list_dir" {
		return []*SessionUpdate{ReadUpdate(stepRow, cwd)}
	}
	if name == "grep_search" || name == "search_web" {
		return []*SessionUpdate{SearchUpdate(stepRow, cwd)}
	}
	if name == "run_command" {
		return []*SessionUpdate{ExecuteUpdate(stepRow, cwd)}
	}
	if name == "read_url_content" {
		return []*SessionUpdate{FetchUpdate(stepRow)}
	}
	if name == "invoke_subagent" {
		return []*SessionUpdate{SubagentUpdate(stepRow)}
	}
	if name == "ask_question" {
		return []*SessionUpdate{QuestionUpdate(stepRow)}
	}
	if strings.Contains(name, "write") || strings.Contains(name, "replace") || strings.Contains(name, "edit") || strings.Contains(name, "patch") {
		return EditUpdate(stepRow, cwd)
	}
	return []*SessionUpdate{OtherUpdate(stepRow)}
}

var LifecycleStepTypes = map[int64]bool{
	90:  true,
	98:  true,
	101: true,
}

func BuildUpdateFromStepPayload(stepRow *StepRow, cwd string) []*SessionUpdate {
	switch stepRow.StepType {
	case 14:
		return UserPromptUpdate(stepRow)
	case 15:
		return []*SessionUpdate{AgentUpdate(stepRow)}
	case 23:
		return TitleUpdateFunc(stepRow)
	case 5:
		return EditUpdate(stepRow, cwd)
	case 17:
		return buildByToolName(stepRow, cwd)
	case 8, 9:
		return []*SessionUpdate{ReadUpdate(stepRow, cwd)}
	case 7, 33:
		return []*SessionUpdate{SearchUpdate(stepRow, cwd)}
	case 21:
		return []*SessionUpdate{ExecuteUpdate(stepRow, cwd)}
	case 31:
		return []*SessionUpdate{FetchUpdate(stepRow)}
	case 127:
		return []*SessionUpdate{SubagentUpdate(stepRow)}
	case 138:
		return []*SessionUpdate{QuestionUpdate(stepRow)}
	case 132:
		return []*SessionUpdate{OtherUpdate(stepRow)}
	case 90, 98, 101:
		return nil
	default:
		if LifecycleStepTypes[stepRow.StepType] {
			return nil
		}
		return buildByToolName(stepRow, cwd)
	}
}
