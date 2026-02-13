package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lowkaihon/cli-coding-agent/llm"
)

// SessionMeta holds metadata about a saved session.
type SessionMeta struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Preview   string    `json:"preview"`
	MsgCount  int       `json:"msg_count"`
}

// SessionFile is the on-disk representation of a session.
type SessionFile struct {
	Meta     SessionMeta   `json:"meta"`
	Messages []llm.Message `json:"messages"`
	Tasks    []Task        `json:"tasks,omitempty"`
}

func generateSessionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

func sessionsDir(workDir string) (string, error) {
	return globalSessionsDir(workDir)
}

// SaveSession persists the current conversation (excluding system prompt) to disk.
// Errors are returned but callers should treat them as non-fatal.
func (a *Agent) SaveSession() error {
	// Skip if only system prompt exists
	if len(a.messages) <= 1 {
		return nil
	}

	dir, err := sessionsDir(a.workDir)
	if err != nil {
		return fmt.Errorf("resolve sessions dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	// Build preview from first user message
	preview := ""
	for _, msg := range a.messages {
		if msg.Role == "user" && msg.Content != nil && *msg.Content != "" {
			preview = *msg.Content
			if len(preview) > 100 {
				preview = preview[:100]
			}
			break
		}
	}

	saved := a.messages[1:] // exclude system prompt
	now := time.Now()

	sf := SessionFile{
		Meta: SessionMeta{
			ID:        a.sessionID,
			CreatedAt: a.sessionCreated,
			UpdatedAt: now,
			Preview:   preview,
			MsgCount:  len(saved),
		},
		Messages: saved,
		Tasks:    a.tasks,
	}

	data, err := json.Marshal(sf)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	path := filepath.Join(dir, a.sessionID+".json")
	return atomicWriteSession(path, data)
}

func atomicWriteSession(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ResumeSession loads a saved session and rebuilds the message history
// with a fresh system prompt.
func (a *Agent) ResumeSession(sessionID string) error {
	dir, err := sessionsDir(a.workDir)
	if err != nil {
		return fmt.Errorf("resolve sessions dir: %w", err)
	}
	path := filepath.Join(dir, sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}

	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("parse session: %w", err)
	}

	// Rebuild: fresh system prompt + saved messages
	a.messages = make([]llm.Message, 0, 1+len(sf.Messages))
	a.messages = append(a.messages, llm.TextMessage("system", a.systemPrompt()))
	a.messages = append(a.messages, sf.Messages...)
	a.sessionID = sf.Meta.ID
	a.sessionCreated = sf.Meta.CreatedAt
	a.tasks = sf.Tasks
	a.lastTokensUsed = 0
	a.rebuildCheckpoints()
	return nil
}

// ListSessions reads all session files from the sessions directory,
// returning up to max entries sorted by UpdatedAt descending.
func ListSessions(workDir string, max int) ([]SessionMeta, error) {
	dir, err := sessionsDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sessions dir: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var metas []SessionMeta
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var sf SessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		metas = append(metas, sf.Meta)
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})

	if max > 0 && len(metas) > max {
		metas = metas[:max]
	}
	return metas, nil
}
