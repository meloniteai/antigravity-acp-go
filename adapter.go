package antigravityacp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type PromptOutcome struct {
	StopReason     string
	ConversationID string
	LastStepIdx    int64
	HadUpdates     bool
	Error          string
}

type Client interface {
	Update(sessionID string, update *SessionUpdate) error
	RequestPermission(params interface{}) (interface{}, error)
}

type Adapter struct {
	mu               sync.Mutex
	binary           string
	conversationsDir string
	workingDir       string
	skipNarration    bool
	children         map[string]*exec.Cmd
	cancelled        map[string]bool
}

func NewAdapter(binary, conversationsDir, workingDir string, skipNarration bool) *Adapter {
	return &Adapter{
		binary:           binary,
		conversationsDir: conversationsDir,
		workingDir:       workingDir,
		skipNarration:    skipNarration,
		children:         make(map[string]*exec.Cmd),
		cancelled:        make(map[string]bool),
	}
}

func (a *Adapter) Cancel(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled[sessionID] = true
	cmd := a.children[sessionID]
	if cmd != nil && cmd.Process != nil {
		if runtime.GOOS == "windows" {
			_ = cmd.Process.Kill()
		} else {
			_ = cmd.Process.Signal(os.Interrupt)
		}
	}
}

func (a *Adapter) RunPrompt(sessionID string, session *Session, promptText string, client Client) (*PromptOutcome, error) {
	a.mu.Lock()
	delete(a.cancelled, sessionID)
	effectiveCwd := session.CWD
	if effectiveCwd == "" {
		effectiveCwd = a.workingDir
	}

	var snapshot map[string]bool
	if session.ConversationID == "" {
		snapshot = ConversationSnapshot(a.conversationsDir)
	}
	a.mu.Unlock()

	args := []string{"--add-dir", effectiveCwd}
	for _, dir := range session.AdditionalDirs {
		args = append(args, "--add-dir", dir)
	}

	if extra := os.Getenv("AGY_EXTRA_ARGS"); extra != "" {
		for _, arg := range strings.Fields(extra) {
			args = append(args, arg)
		}
	}

	if session.ConversationID != "" {
		args = append(args, "--conversation", session.ConversationID)
	}
	if session.ModelID != "" {
		args = append(args, "--model", session.ModelID)
	}

	pm := session.PermissionMode
	if pm == "bypassPermissions" || pm == "bypass" || pm == "dontAsk" {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "-p", promptText)

	cmd := exec.Command(a.binary, args...)
	cmd.Dir = effectiveCwd
	cmd.Env = append(os.Environ(), "AGY_CONVERSATIONS_DIR="+a.conversationsDir)

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = io.Discard
	cmd.Stdin = nil

	a.mu.Lock()
	a.children[sessionID] = cmd
	a.mu.Unlock()

	if err := cmd.Start(); err != nil {
		a.mu.Lock()
		delete(a.children, sessionID)
		a.mu.Unlock()
		return nil, err
	}

	pollDone := make(chan struct{})
	var pollErr error

	var boundID string
	a.mu.Lock()
	if session.ConversationID != "" {
		boundID = session.ConversationID
	}
	a.mu.Unlock()

	translator := NewTranslator(TranslatorOptions{
		Mode:          ModeStream,
		SkipNarration: a.skipNarration,
		CWD:           effectiveCwd,
	})

	var dbConn *ConversationDb
	var dbMu sync.Mutex

	pollOnce := func() {
		dbMu.Lock()
		defer dbMu.Unlock()
		if boundID == "" && snapshot != nil {
			boundID = NewConversationID(a.conversationsDir, snapshot)
		}
		if boundID == "" {
			return
		}
		if dbConn == nil {
			var err error
			dbConn, err = OpenConversationDb(a.conversationsDir, boundID)
			if err != nil {
				return
			}
		}
		rows, err := dbConn.ReadAfter(session.LastStepIdx)
		if err != nil {
			pollErr = err
			return
		}
		updates := translator.Translate(rows)
		for _, update := range updates {
			_ = client.Update(sessionID, update)
		}
		if len(rows) > 0 {
			session.LastStepIdx = translator.LastStepIdx()
		}
	}

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pollDone:
				return
			case <-ticker.C:
				pollOnce()
			}
		}
	}()

	err := cmd.Wait()
	close(pollDone)

	a.mu.Lock()
	delete(a.children, sessionID)
	wasCancelled := a.cancelled[sessionID]
	delete(a.cancelled, sessionID)
	a.mu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		pollOnce()
		time.Sleep(100 * time.Millisecond)
	}

	dbMu.Lock()
	if dbConn != nil {
		dbConn.Close()
	}
	dbMu.Unlock()

	stderr := strings.TrimSpace(stderrBuf.String())
	if stderr != "" {
		_, _ = fmt.Fprintln(os.Stderr, "[agy-acp] agy stderr:", stderr)
	}

	stopReason := "end_turn"
	if wasCancelled {
		stopReason = "cancelled"
	}

	outcome := &PromptOutcome{
		StopReason:     stopReason,
		ConversationID: boundID,
		LastStepIdx:    session.LastStepIdx,
		HadUpdates:     translator.HadUpdates(),
	}

	if !wasCancelled && err != nil {
		if !translator.HadUpdates() {
			if stderr != "" {
				outcome.Error = fmt.Sprintf("agy failed: %s", stderr)
			} else {
				outcome.Error = fmt.Sprintf("agy exited with status: %v", err)
			}
		}
	}
	_ = pollErr

	return outcome, nil
}
