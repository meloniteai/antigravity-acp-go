package antigravityacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type ClientConn struct {
	mu  sync.Mutex
	out io.Writer
}

func NewClientConn(out io.Writer) *ClientConn {
	return &ClientConn{out: out}
}

func (c *ClientConn) Update(sessionID string, update *SessionUpdate) error {
	type UpdateParams struct {
		SessionID string         `json:"sessionId"`
		Update    *SessionUpdate `json:"update"`
	}
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params": UpdateParams{
			SessionID: sessionID,
			Update:    update,
		},
	}
	data, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.out.Write(append(data, '\n'))
	return err
}

func (c *ClientConn) RequestPermission(params interface{}) (interface{}, error) {
	return nil, nil
}

type Server struct {
	agent *AgyAcpAgent
}

func NewServer(agent *AgyAcpAgent) *Server {
	return &Server{agent: agent}
}

func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	client := &ClientConn{out: out}
	scanner := bufio.NewScanner(in)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var raw rawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			s.writeError(out, nil, -32700, "Parse error")
			continue
		}

		var id interface{}
		if len(raw.ID) > 0 {
			_ = json.Unmarshal(raw.ID, &id)
		}

		isRequest := len(raw.ID) > 0

		if raw.Method == "" {
			if isRequest {
				s.writeError(out, id, -32600, "Invalid Request")
			}
			continue
		}

		if isRequest {
			go func(method string, params json.RawMessage, idVal interface{}) {
				res, err := s.handleRequest(method, params, client)
				if err != nil {
					s.writeError(out, idVal, -32603, err.Error())
				} else {
					s.writeResult(out, idVal, res)
				}
			}(raw.Method, raw.Params, id)
		} else {
			go s.handleNotification(raw.Method, raw.Params)
		}
	}

	return scanner.Err()
}

func (s *Server) handleRequest(method string, params json.RawMessage, client Client) (interface{}, error) {
	switch method {
	case "agent/initialize":
		return s.agent.Initialize(), nil

	case "agent/authenticate":
		var p struct {
			MethodID string `json:"methodId"`
		}
		_ = json.Unmarshal(params, &p)
		err := s.agent.Authenticate(p.MethodID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{}, nil

	case "agent/logout":
		s.agent.Logout()
		return map[string]interface{}{}, nil

	case "session/new":
		var p struct {
			CWD                   string   `json:"cwd"`
			AdditionalDirectories []string `json:"additionalDirectories"`
		}
		_ = json.Unmarshal(params, &p)
		sessionID, opts := s.agent.NewSession(p.CWD, p.AdditionalDirectories, client)
		return map[string]interface{}{
			"sessionId":     sessionID,
			"configOptions": opts,
		}, nil

	case "session/load":
		var p struct {
			SessionID             string   `json:"sessionId"`
			CWD                   string   `json:"cwd"`
			AdditionalDirectories []string `json:"additionalDirectories"`
		}
		_ = json.Unmarshal(params, &p)
		opts, err := s.agent.LoadSession(p.SessionID, p.CWD, p.AdditionalDirectories, client)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"configOptions": opts,
		}, nil

	case "session/resume":
		var p struct {
			SessionID             string   `json:"sessionId"`
			CWD                   string   `json:"cwd"`
			AdditionalDirectories []string `json:"additionalDirectories"`
		}
		_ = json.Unmarshal(params, &p)
		opts, err := s.agent.ResumeSession(p.SessionID, p.CWD, p.AdditionalDirectories, client)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"configOptions": opts,
		}, nil

	case "session/list":
		var p struct {
			CWD string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &p)
		sessions, err := s.agent.ListSessions(p.CWD)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"sessions": sessions,
		}, nil

	case "session/delete":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		deleted, err := s.agent.DeleteSession(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !deleted {
			return nil, fmt.Errorf("session not found: %s", p.SessionID)
		}
		return map[string]interface{}{}, nil

	case "session/close":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		s.agent.CloseSession(p.SessionID)
		return map[string]interface{}{}, nil

	case "session/prompt":
		var p struct {
			SessionID string      `json:"sessionId"`
			Prompt    interface{} `json:"prompt"`
		}
		_ = json.Unmarshal(params, &p)
		outcome, err := s.agent.Prompt(p.SessionID, p.Prompt, client)
		if err != nil {
			return nil, err
		}
		if outcome.Error != "" {
			return nil, errors.New(outcome.Error)
		}
		return map[string]interface{}{
			"stopReason": outcome.StopReason,
		}, nil

	case "session/setConfigOption":
		var p struct {
			SessionID string      `json:"sessionId"`
			ConfigID  string      `json:"configId"`
			Value     interface{} `json:"value"`
		}
		_ = json.Unmarshal(params, &p)

		valStr := ""
		if str, ok := p.Value.(string); ok {
			valStr = str
		} else if p.Value != nil {
			valStr = fmt.Sprintf("%v", p.Value)
		}

		opts, err := s.agent.SetConfigOption(p.SessionID, p.ConfigID, valStr)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"configOptions": opts,
		}, nil

	case "resources/list":
		return map[string]interface{}{"resources": []interface{}{}}, nil

	case "prompts/list":
		return map[string]interface{}{"prompts": []interface{}{}}, nil

	case "tools/list":
		return map[string]interface{}{"tools": []interface{}{}}, nil

	default:
		return nil, fmt.Errorf("Method not found: %s", method)
	}
}

func (s *Server) handleNotification(method string, params json.RawMessage) {
	if method == "session/cancel" {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		if p.SessionID != "" {
			s.agent.Cancel(p.SessionID)
		}
	}
}

func (s *Server) writeResult(out io.Writer, id interface{}, result interface{}) {
	res := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(res)
	_, _ = out.Write(append(data, '\n'))
}

func (s *Server) writeError(out io.Writer, id interface{}, code int, msg string) {
	res := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: msg,
		},
	}
	data, _ := json.Marshal(res)
	_, _ = out.Write(append(data, '\n'))
}
