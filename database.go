package antigravityacp

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type DbStat struct {
	MtimeMs int64
	Size    int64
}

func ConversationDbPath(dir, id string) string {
	return filepath.Join(dir, id+".db")
}

func StatConversation(dir, id string) (*DbStat, error) {
	p := ConversationDbPath(dir, id)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return &DbStat{
		MtimeMs: fi.ModTime().UnixNano() / 1e6,
		Size:    fi.Size(),
	}, nil
}

type ConversationDb struct {
	db   *sql.DB
	stmt *sql.Stmt
}

func OpenConversationDb(dir, id string) (*ConversationDb, error) {
	dbPath := ConversationDbPath(dir, id)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='steps'").Scan(&count)
	if err != nil || count == 0 {
		db.Close()
		return nil, fmt.Errorf("steps table not found in steps db")
	}

	query := "SELECT idx, step_type, status, step_payload, error_details, permissions, task_details FROM steps WHERE idx > ? ORDER BY idx"
	stmt, err := db.Prepare(query)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &ConversationDb{db: db, stmt: stmt}, nil
}

func (c *ConversationDb) ReadAfter(afterStepIdx int64) ([]*StepRow, error) {
	rows, err := c.stmt.Query(afterStepIdx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stepRows []*StepRow
	for rows.Next() {
		var idx, stepType, status int64
		var stepPayloadRaw, errorDetailsRaw, permissionsRaw, taskDetailsRaw []byte

		err := rows.Scan(&idx, &stepType, &status, &stepPayloadRaw, &errorDetailsRaw, &permissionsRaw, &taskDetailsRaw)
		if err != nil {
			return nil, err
		}

		payload, err := DecodeStepPayload(stepPayloadRaw)
		if err != nil {
			return nil, err
		}

		var errorDetails *ErrorDetails
		if len(errorDetailsRaw) > 0 {
			errorDetails, _ = DecodeErrorDetails(errorDetailsRaw)
		}

		var permissionInfo *PermissionInfo
		if len(permissionsRaw) > 0 {
			permissionInfo, _ = DecodePermissions(permissionsRaw)
		}

		var taskDetails *TaskDetails
		if len(taskDetailsRaw) > 0 {
			taskDetails, _ = DecodeTaskDetails(taskDetailsRaw)
		}

		stepRows = append(stepRows, &StepRow{
			Idx:         idx,
			StepType:    stepType,
			Status:      status,
			StepPayload: payload,
			Error:       errorDetails,
			Permission:  permissionInfo,
			Task:        taskDetails,
		})
	}
	return stepRows, nil
}

func (c *ConversationDb) Close() {
	if c.stmt != nil {
		c.stmt.Close()
	}
	if c.db != nil {
		c.db.Close()
	}
}

func ReadRows(dir, id string, afterStepIdx int64) ([]*StepRow, error) {
	conn, err := OpenConversationDb(dir, id)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.ReadAfter(afterStepIdx)
}

func ConversationSnapshot(dir string) map[string]bool {
	out := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".db") {
			out[strings.TrimSuffix(name, ".db")] = true
		}
	}
	return out
}

func NewConversationID(dir string, before map[string]bool) string {
	snapshot := ConversationSnapshot(dir)
	var created []string
	for id := range snapshot {
		if !before[id] {
			created = append(created, id)
		}
	}
	if len(created) == 1 {
		return created[0]
	}
	return ""
}
