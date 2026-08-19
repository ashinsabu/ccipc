package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ChatMsg is one turn in the conversation history.
type ChatMsg struct {
	Role    string `json:"role"`    // "user" | "assistant"
	From    string `json:"from"`
	Content string `json:"content"`
	TS      string `json:"ts"`
}

// ThreadHistory persists the full message history for an agent pair so that
// each claude -p invocation gets full context without needing a resumed session.
type ThreadHistory struct {
	Key      string    `json:"key"`
	MyID     string    `json:"my_id"`
	Messages []ChatMsg `json:"messages"`
}

// historyMu guards per-thread file writes. Key = ThreadKey.
var (
	historyMu   sync.Map // map[string]*sync.Mutex
)

func threadMutex(key string) *sync.Mutex {
	v, _ := historyMu.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func loadHistory(myID, partnerID string) (ThreadHistory, error) {
	key := ThreadKey(myID, partnerID)
	path := filepath.Join(ipcDir(), "threads", key+".json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ThreadHistory{Key: key, MyID: myID}, nil
	}
	if err != nil {
		return ThreadHistory{}, err
	}
	var h ThreadHistory
	return h, json.Unmarshal(data, &h)
}

func saveHistory(h ThreadHistory) error {
	dir := filepath.Join(ipcDir(), "threads")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, h.Key+".json"), data, 0644)
}

const maxHistoryTurns = 30

func buildPrompt(myAgent Agent, msg Message, history ThreadHistory) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are Claude agent %q", myAgent.ID))
	if myAgent.Repo != "" {
		sb.WriteString(fmt.Sprintf(", working in the %q repository", myAgent.Repo))
	}
	if myAgent.Branch != "" {
		sb.WriteString(fmt.Sprintf(" on branch %q", myAgent.Branch))
	}
	sb.WriteString(".\n\n")
	sb.WriteString(fmt.Sprintf("You are having a work conversation with agent %q. ", msg.From))
	sb.WriteString("Respond like a knowledgeable colleague: specific, helpful, direct. ")
	sb.WriteString("Use your tools to read files or run commands when the conversation calls for it. ")
	sb.WriteString("Do not add filler like \"Sure!\" or \"Of course!\". Just respond.\n\n")

	if len(history.Messages) > 0 {
		msgs := history.Messages
		if len(msgs) > maxHistoryTurns {
			msgs = msgs[len(msgs)-maxHistoryTurns:]
			sb.WriteString(fmt.Sprintf("(showing last %d turns)\n\n", maxHistoryTurns))
		}
		sb.WriteString("CONVERSATION SO FAR:\n")
		for _, cm := range msgs {
			label := cm.From
			if cm.Role == "assistant" {
				label += " (you)"
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", label, cm.Content))
		}
		sb.WriteString("---\n\n")
	}

	sb.WriteString(fmt.Sprintf("NEW MESSAGE from %s:\n%s\n\nYour response:", msg.From, msg.Body))
	return sb.String()
}

// AskOnce sends a question to a partner agent by spawning claude -p in their CWD
// with thread history as context, and returns the answer directly — no daemon needed.
func AskOnce(myAgent, partner Agent, question string) (string, error) {
	mu := threadMutex(ThreadKey(myAgent.ID, partner.ID))
	mu.Lock()
	defer mu.Unlock()

	history, err := loadHistory(myAgent.ID, partner.ID)
	if err != nil {
		return "", fmt.Errorf("load history: %w", err)
	}

	msg := Message{From: myAgent.ID, Body: question}
	prompt := buildPrompt(partner, msg, history)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--output-format", "text"}
	cmd := exec.CommandContext(ctx, "claude", args...)
	if partner.CWD != "" {
		cmd.Dir = partner.CWD
	}
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude -p timed out after 5m")
		}
		return "", fmt.Errorf("claude -p: %w", err)
	}

	response := strings.TrimSpace(string(out))
	if response == "" {
		return "", fmt.Errorf("claude -p returned empty response")
	}

	history.Messages = append(history.Messages,
		ChatMsg{Role: "user", From: myAgent.ID, Content: question, TS: time.Now().UTC().Format(time.RFC3339)},
		ChatMsg{Role: "assistant", From: partner.ID, Content: response, TS: time.Now().UTC().Format(time.RFC3339)},
	)
	if err := saveHistory(history); err != nil {
		fmt.Fprintf(os.Stderr, "[ask] warn: save history: %v\n", err)
	}

	return response, nil
}

// RespondOnce handles a single incoming message: generates a reply via claude -p,
// sends it back, and acks the original.
func RespondOnce(myAgent Agent, msg Message, verbose bool) error {
	mu := threadMutex(ThreadKey(myAgent.ID, msg.From))
	mu.Lock()
	defer mu.Unlock()

	history, err := loadHistory(myAgent.ID, msg.From)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	prompt := buildPrompt(myAgent, msg, history)

	if verbose {
		fmt.Printf("[respond] processing msg=%s from=%s\n", msg.ID, msg.From)
	}

	// 5-minute hard timeout so the daemon never hangs indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--output-format", "text"}
	cmd := exec.CommandContext(ctx, "claude", args...)
	if myAgent.CWD != "" {
		cmd.Dir = myAgent.CWD
	}
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("claude -p timed out after 5m")
		}
		return fmt.Errorf("claude -p: %w", err)
	}

	response := strings.TrimSpace(string(out))
	if response == "" {
		return fmt.Errorf("claude -p returned empty response")
	}

	if verbose {
		preview := response
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("[respond] reply (%d chars): %s\n", len(response), preview)
	}

	// Persist conversation history (still under the thread mutex).
	history.Messages = append(history.Messages,
		ChatMsg{Role: "user", From: msg.From, Content: msg.Body, TS: msg.TS},
		ChatMsg{Role: "assistant", From: myAgent.ID, Content: response, TS: time.Now().UTC().Format(time.RFC3339)},
	)
	if err := saveHistory(history); err != nil {
		fmt.Fprintf(os.Stderr, "[respond] warn: save history: %v\n", err)
	}

	// Send the reply.
	replyTo := msg.ID
	_, err = Send(Message{
		Type:    "reply",
		From:    myAgent.ID,
		To:      msg.From,
		Body:    response,
		Subject: msg.Subject,
		Thread:  msg.Thread,
		ReplyTo: &replyTo,
	})
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}

	// Ack (delete) the original message.
	if err := Ack(myAgent.ID, msg.ID); err != nil {
		fmt.Fprintf(os.Stderr, "[respond] warn: ack %s: %v\n", msg.ID, err)
	}

	return nil
}

// RespondDaemon polls an agent's inbox every 2s and calls RespondOnce for each
// new message. Runs until done is closed.
func RespondDaemon(myAgent Agent, verbose bool, done <-chan struct{}) {
	// Seed seen so we don't reply to messages that already existed at start.
	seen := map[string]bool{}
	existing, _ := Read(myAgent.ID)
	for _, m := range existing {
		seen[m.ID] = true
	}

	fmt.Printf("[respond] agent=%s  cwd=%s\n", myAgent.ID, myAgent.CWD)
	if len(existing) > 0 {
		fmt.Printf("[respond] skipping %d pre-existing messages\n", len(existing))
	}
	fmt.Println("[respond] watching inbox... (Ctrl+C to stop)")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			msgs, err := Read(myAgent.ID)
			if err != nil {
				continue
			}
			for _, m := range msgs {
				if seen[m.ID] {
					continue
				}
				// Mark seen before spawning goroutine to prevent double-processing
				// across consecutive polls while a slow claude -p is still running.
				seen[m.ID] = true
				go func(msg Message) {
					if err := RespondOnce(myAgent, msg, verbose); err != nil {
						fmt.Fprintf(os.Stderr, "[respond] error for msg=%s: %v\n", msg.ID, err)
					}
				}(m)
			}
		}
	}
}
