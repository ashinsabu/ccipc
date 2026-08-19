package ipc

import (
	"sync"
	"time"
)

// ThreadKey returns a canonical key for a conversation between two agents.
// Alphabetical ordering means A↔B and B↔A share the same key.
func ThreadKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return sanitize(a) + "--" + sanitize(b)
}

// InboxPoller watches an agent's inbox for new messages from a specific sender.
type InboxPoller struct {
	AgentID  string
	FromID   string // filter to this sender; empty = all senders
	Interval time.Duration
	seen     map[string]bool
	mu       sync.Mutex
}

func NewPoller(agentID, fromID string) *InboxPoller {
	return &InboxPoller{
		AgentID:  agentID,
		FromID:   fromID,
		Interval: 1500 * time.Millisecond,
		seen:     map[string]bool{},
	}
}

// Start polls in a background goroutine. Stops when done is closed.
// Existing messages are seeded as already-seen so we don't replay on startup.
func (p *InboxPoller) Start(done <-chan struct{}, onMsg func(Message)) {
	existing, _ := Read(p.AgentID)
	p.mu.Lock()
	for _, m := range existing {
		p.seen[m.ID] = true
	}
	p.mu.Unlock()

	go func() {
		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				msgs, err := Read(p.AgentID)
				if err != nil {
					continue
				}
				p.mu.Lock()
				var fresh []Message
				for _, m := range msgs {
					if p.seen[m.ID] {
						continue
					}
					if p.FromID != "" && m.From != p.FromID {
						continue
					}
					p.seen[m.ID] = true
					fresh = append(fresh, m)
				}
				p.mu.Unlock()
				for _, m := range fresh {
					onMsg(m)
				}
			}
		}
	}()
}
