package antigravityacp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNarration(t *testing.T) {
	if !IsNarration("I will do X") {
		t.Error("expected true for 'I will do X'")
	}
	if !IsNarration("I'll clean up\nI’ll start the process") {
		t.Error("expected true for narration prefix combination")
	}
	if IsNarration("Hello, world!") {
		t.Error("expected false for 'Hello, world!'")
	}

	filtered := FilterNarration([]string{"I will do X", "Hello world", "I'll do Y"})
	if filtered != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", filtered)
	}
}

func TestProtobufVarint(t *testing.T) {
	buf := []byte{0x08, 0x96, 0x01}
	r := NewProtoReader(buf)
	tag, err := r.Varint()
	if err != nil {
		t.Fatal(err)
	}
	if tag != 8 {
		t.Errorf("expected tag 8, got %d", tag)
	}
	val, err := r.Varint()
	if err != nil {
		t.Fatal(err)
	}
	if val != 150 {
		t.Errorf("expected value 150, got %d", val)
	}
}

func TestProtobufStepPayload(t *testing.T) {
	data := []byte{8, 42}
	payload, err := DecodeStepPayload(data)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ValidityCheck != 42 {
		t.Errorf("expected validityCheck 42, got %d", payload.ValidityCheck)
	}
}

func TestE2EAcpServer(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agy-acp-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sessionsFile := filepath.Join(tmpDir, "sessions.json")
	store := NewSessionStore(sessionsFile, tmpDir)

	agent := NewAgyAcpAgent("mock-agy", tmpDir, tmpDir, false, "1.0.0", store)
	agent.availableModels = []string{"model1", "model2"}

	server := NewServer(agent)

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = server.Run(ctx, inReader, outWriter)
	}()

	sendRequest := func(method string, params interface{}, id interface{}) map[string]interface{} {
		idBytes, _ := json.Marshal(id)
		paramsBytes, _ := json.Marshal(params)
		req := rawMessage{
			JSONRPC: "2.0",
			ID:      idBytes,
			Method:  method,
			Params:  paramsBytes,
		}
		reqBytes, _ := json.Marshal(req)
		_, _ = inWriter.Write(append(reqBytes, '\n'))

		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			var resp map[string]interface{}
			_ = json.Unmarshal(scanner.Bytes(), &resp)
			respID, hasID := resp["id"]
			if hasID {
				var match bool
				switch v := id.(type) {
				case int:
					f, ok := respID.(float64)
					match = ok && f == float64(v)
				case string:
					s, ok := respID.(string)
					match = ok && s == v
				}
				if match {
					return resp
				}
			}
		}
		return nil
	}

	// 1. Initialize
	resp := sendRequest("agent/initialize", map[string]interface{}{}, 1)
	if resp == nil {
		t.Fatal("no response for agent/initialize")
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result, got response: %v", resp)
	}
	agentInfo, ok := result["agentInfo"].(map[string]interface{})
	if !ok || agentInfo["name"] != "Antigravity" {
		t.Errorf("unexpected agentInfo: %v", agentInfo)
	}

	// 2. Session new
	resp = sendRequest("session/new", map[string]interface{}{
		"cwd": tmpDir,
	}, 2)
	result, _ = resp["result"].(map[string]interface{})
	sessionID := result["sessionId"].(string)
	if sessionID == "" {
		t.Error("expected session id")
	}

	// 3. Set config option
	resp = sendRequest("session/setConfigOption", map[string]interface{}{
		"sessionId": sessionID,
		"configId":  "mode",
		"value":     "plan",
	}, 3)
	if resp["result"] == nil {
		t.Errorf("expected result for config option, got: %v", resp)
	}

	// 4. List sessions
	resp = sendRequest("session/list", map[string]interface{}{}, 4)
	result, _ = resp["result"].(map[string]interface{})
	sessions, _ := result["sessions"].([]interface{})
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}

	// 5. Delete session
	resp = sendRequest("session/delete", map[string]interface{}{
		"sessionId": sessionID,
	}, 5)
	if resp["result"] == nil {
		t.Errorf("expected successful deletion, got: %v", resp)
	}

	// 6. List sessions again
	resp = sendRequest("session/list", map[string]interface{}{}, 6)
	result, _ = resp["result"].(map[string]interface{})
	sessions, _ = result["sessions"].([]interface{})
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions after deletion, got %d", len(sessions))
	}
}
