package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Message struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"` // ask | reply | notify | handoff | ack
	From    string         `json:"from"`
	To      string         `json:"to"`
	Subject string         `json:"subject,omitempty"`
	Body    string         `json:"body"`
	Ctx     map[string]any `json:"ctx,omitempty"`
	Thread  string         `json:"thread"`
	ReplyTo *string        `json:"replyTo"`
	TS      string         `json:"ts"`
	V       int            `json:"v"`
	Ext     map[string]any `json:"ext"`
}

func ipcDir() string {
	return filepath.Join(os.Getenv("HOME"), ".claude", "ipc")
}

func InboxPath(agentID string) string {
	return filepath.Join(ipcDir(), "inbox", sanitize(agentID))
}

func sanitize(id string) string {
	return strings.NewReplacer("/", "--", " ", "_").Replace(id)
}

func newMsgID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "msg_" + hex.EncodeToString(b)
}

func Send(msg Message) (Message, error) {
	if msg.To == "" {
		return msg, fmt.Errorf("message missing 'to' field")
	}
	if msg.Type == "" {
		msg.Type = "ask"
	}
	if msg.From == "" {
		a, err := AutoAgent("")
		if err == nil {
			msg.From = a.ID
		}
	}

	msg.ID = newMsgID()
	msg.TS = time.Now().UTC().Format(time.RFC3339)
	msg.V = 1
	if msg.Thread == "" {
		msg.Thread = msg.ID
	}
	if msg.Ext == nil {
		msg.Ext = map[string]any{}
	}

	inboxDir := InboxPath(msg.To)
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return msg, err
	}

	ts := time.Now().UTC().Format("20060102T150405")
	filename := fmt.Sprintf("%s-%s.json", ts, msg.ID)

	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return msg, err
	}
	return msg, os.WriteFile(filepath.Join(inboxDir, filename), data, 0644)
}

func Read(agentID string) ([]Message, error) {
	dir := InboxPath(agentID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var msgs []Message
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m Message
		if json.Unmarshal(data, &m) == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

func Ack(agentID, msgID string) error {
	dir := InboxPath(agentID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), msgID) {
			return os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return fmt.Errorf("message %s not found in inbox for %s", msgID, agentID)
}
