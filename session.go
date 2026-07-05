package antigravityacp

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Session struct {
	ConversationID string   `json:"conversationId"`
	LastStepIdx    int64    `json:"lastStepIdx"`
	ModelID        string   `json:"modelId"`
	PermissionMode string   `json:"permissionMode"`
	CWD            string   `json:"cwd"`
	AdditionalDirs []string `json:"additionalDirs"`
	Title          string   `json:"title"`
	UpdatedAt      string   `json:"updatedAt"`
}

func (s *Session) UnmarshalJSON(data []byte) error {
	type Alias Session
	var aux struct {
		Alias
		ConversationIDSnake *string  `json:"conversation_id"`
		LastStepIdxSnake    *int64   `json:"last_step_idx"`
		ModelIDSnake        *string  `json:"model_id"`
		PermissionModeSnake *string  `json:"permission_mode"`
		AdditionalDirsSnake []string `json:"additional_dirs"`
		CWDSnake            *string  `json:"cwd_snake"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*s = Session(aux.Alias)
	if aux.ConversationIDSnake != nil && s.ConversationID == "" {
		s.ConversationID = *aux.ConversationIDSnake
	}
	if aux.LastStepIdxSnake != nil && s.LastStepIdx == 0 {
		s.LastStepIdx = *aux.LastStepIdxSnake
	}
	if aux.ModelIDSnake != nil && s.ModelID == "" {
		s.ModelID = *aux.ModelIDSnake
	}
	if aux.PermissionModeSnake != nil && s.PermissionMode == "" {
		s.PermissionMode = *aux.PermissionModeSnake
	}
	if len(aux.AdditionalDirsSnake) > 0 && len(s.AdditionalDirs) == 0 {
		s.AdditionalDirs = aux.AdditionalDirsSnake
	}
	if aux.CWDSnake != nil && s.CWD == "" {
		s.CWD = *aux.CWDSnake
	}
	if s.LastStepIdx == 0 {
		s.LastStepIdx = -1
	}
	return nil
}

type SessionStore struct {
	mu   sync.Mutex
	file string
	dir  string
}

func NewSessionStore(file, dir string) *SessionStore {
	return &SessionStore{file: file, dir: dir}
}

type diskStore struct {
	Sessions map[string]*Session `json:"sessions"`
}

func (s *SessionStore) load() (*diskStore, error) {
	if _, err := os.Stat(s.file); os.IsNotExist(err) {
		return &diskStore{Sessions: make(map[string]*Session)}, nil
	}
	data, err := os.ReadFile(s.file)
	if err != nil {
		return nil, err
	}
	var store diskStore
	if err := json.Unmarshal(data, &store); err != nil {
		var raw struct {
			Sessions map[string]json.RawMessage `json:"sessions"`
		}
		if err2 := json.Unmarshal(data, &raw); err2 == nil && raw.Sessions != nil {
			store.Sessions = make(map[string]*Session)
			for k, v := range raw.Sessions {
				var sess Session
				if err3 := json.Unmarshal(v, &sess); err3 == nil {
					store.Sessions[k] = &sess
				}
			}
			return &store, nil
		}
		return &diskStore{Sessions: make(map[string]*Session)}, nil
	}
	if store.Sessions == nil {
		store.Sessions = make(map[string]*Session)
	}
	return &store, nil
}

func (s *SessionStore) write(store *diskStore) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	tmpFile := s.file + ".tmp"
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, s.file)
}

func (s *SessionStore) Restore(sessionID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.load()
	if err != nil {
		return nil, err
	}
	sess, exists := store.Sessions[sessionID]
	if !exists {
		return nil, nil
	}
	return sess, nil
}

type SessionListEntry struct {
	SessionID string   `json:"sessionId"`
	Session   *Session `json:"session"`
}

func (s *SessionStore) List() ([]SessionListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.load()
	if err != nil {
		return nil, err
	}
	var entries []SessionListEntry
	for id, sess := range store.Sessions {
		entries = append(entries, SessionListEntry{SessionID: id, Session: sess})
	}
	return entries, nil
}

func (s *SessionStore) Delete(sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.load()
	if err != nil {
		return false, err
	}
	if _, exists := store.Sessions[sessionID]; !exists {
		return false, nil
	}
	delete(store.Sessions, sessionID)
	err = s.write(store)
	return err == nil, err
}

func (s *SessionStore) Persist(sessionID string, session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.load()
	if err != nil {
		return err
	}
	store.Sessions[sessionID] = session
	return s.write(store)
}

type SessionManager struct {
	store    *SessionStore
	sessions map[string]*Session
	order    []string
	maxSize  int
}

func NewSessionManager(store *SessionStore, maxSize int) *SessionManager {
	return &SessionManager{
		store:    store,
		sessions: make(map[string]*Session),
		maxSize:  maxSize,
	}
}

func (m *SessionManager) evictIfNeeded() {
	for len(m.sessions) >= m.maxSize {
		if len(m.order) == 0 {
			break
		}
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.sessions, oldest)
	}
}

func (m *SessionManager) Create(cwd string, additionalDirs []string) (string, *Session) {
	sessionID := generateUUID()
	m.evictIfNeeded()
	session := &Session{
		LastStepIdx:    -1,
		CWD:            cwd,
		AdditionalDirs: additionalDirs,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	m.sessions[sessionID] = session
	m.order = append(m.order, sessionID)
	return sessionID, session
}

func (m *SessionManager) Peek(sessionID string) *Session {
	return m.sessions[sessionID]
}

func (m *SessionManager) Ensure(sessionID string) (*Session, error) {
	if existing, exists := m.sessions[sessionID]; exists {
		m.removeFromOrder(sessionID)
		m.order = append(m.order, sessionID)
		return existing, nil
	}
	stored, err := m.store.Restore(sessionID)
	if err != nil || stored == nil {
		return nil, err
	}
	m.evictIfNeeded()
	m.sessions[sessionID] = stored
	m.order = append(m.order, sessionID)
	return stored, nil
}

func (m *SessionManager) Adopt(sessionID string, session *Session) {
	m.evictIfNeeded()
	m.sessions[sessionID] = session
	m.removeFromOrder(sessionID)
	m.order = append(m.order, sessionID)
}

func (m *SessionManager) Evict(sessionID string) {
	delete(m.sessions, sessionID)
	m.removeFromOrder(sessionID)
}

func (m *SessionManager) List() ([]SessionListEntry, error) {
	return m.store.List()
}

func (m *SessionManager) Delete(sessionID string) (bool, error) {
	_, inMemory := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.removeFromOrder(sessionID)
	inStore, err := m.store.Delete(sessionID)
	return inMemory || inStore, err
}

func (m *SessionManager) Persist(sessionID string, session *Session) error {
	return m.store.Persist(sessionID, session)
}

func (m *SessionManager) removeFromOrder(sessionID string) {
	for i, id := range m.order {
		if id == sessionID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
