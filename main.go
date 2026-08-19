package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/ashinsabu/ccipc/ipc"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "ccipc",
		Short: "Claude Code IPC — local agent message bus",
	}
	root.AddCommand(
		cmdInstall(),
		cmdRegister(),
		cmdDeregister(),
		cmdLS(),
		cmdGC(),
		cmdWhoami(),
		cmdSend(),
		cmdRead(),
		cmdAck(),
		cmdAsk(),
		cmdChat(),
		cmdRespond(),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── install ──────────────────────────────────────────────────────────────────

func cmdInstall() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install ccipc to ~/.local/bin and wire the SessionStart hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			installDir := filepath.Join(os.Getenv("HOME"), ".local", "bin")
			dest := filepath.Join(installDir, "ccipc")

			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("cannot find own executable: %w", err)
			}
			self, _ = filepath.EvalSymlinks(self)

			if self != dest {
				if err := os.MkdirAll(installDir, 0755); err != nil {
					return err
				}
				data, err := os.ReadFile(self)
				if err != nil {
					return err
				}
				if err := os.WriteFile(dest, data, 0755); err != nil {
					return err
				}
				fmt.Printf("Installed → %s\n", dest)
			} else {
				fmt.Printf("Already at %s\n", dest)
			}

			if !strings.Contains(os.Getenv("PATH"), installDir) {
				fmt.Printf("\nAdd to your shell profile:\n  export PATH=\"$PATH:%s\"\n", installDir)
			}

			if err := ipc.PatchSettings(dest); err != nil {
				return fmt.Errorf("patch settings: %w", err)
			}
			fmt.Printf("Patched ~/.claude/settings.json with SessionStart hook\n")
			return nil
		},
	}
}

// ── register ─────────────────────────────────────────────────────────────────

func cmdRegister() *cobra.Command {
	var autoFlag bool
	var id, title, jira string
	var caps []string

	c := &cobra.Command{
		Use:   "register",
		Short: "Register this agent in the local registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			var a ipc.Agent
			if autoFlag {
				h := readHookInput()
				if os.Getenv("CLAUDE_SESSION_ID") == "" && h.SessionID != "" {
					_ = os.Setenv("CLAUDE_SESSION_ID", h.SessionID)
				}
				var err error
				a, err = ipc.AutoAgent(h.CWD)
				if err != nil {
					return err
				}
			} else {
				if id == "" {
					return fmt.Errorf("--id required (or use --auto)")
				}
				a = ipc.Agent{ID: id, Title: title, Jira: jira, Caps: caps}
			}
			if err := ipc.Register(a); err != nil {
				return err
			}
			// Prune dead agents on every register so the registry self-cleans.
			_ = gcRegistry(false)
			msgs, _ := ipc.Read(a.ID)

			fmt.Printf("[ccipc] agent-id: %s\n", a.ID)
			if len(msgs) == 0 {
				fmt.Printf("[ccipc] inbox: empty\n")
			} else {
				fmt.Printf("[ccipc] inbox: %d unread\n", len(msgs))
				for _, m := range msgs {
					fmt.Printf("[ccipc]   %-14s  from=%-30s  %s\n", m.ID, m.From, m.Subject)
				}
			}
			fmt.Printf("[ccipc] commands (use via Bash tool):\n")
			fmt.Printf("[ccipc]   ccipc ls                               # see all live agents + their IDs\n")
			fmt.Printf("[ccipc]   ccipc inbox                            # read your messages\n")
			fmt.Printf("[ccipc]   ccipc ack <msg-id>                     # delete a message after reading\n")
			fmt.Printf("[ccipc]   ccipc send --to <id> --body \"...\"      # send async message (fire and forget)\n")
			fmt.Printf("[ccipc]   ccipc ask  --to <id> --body \"...\"      # ask and get answer now (no daemon needed)\n")
			fmt.Printf("[ccipc]   ccipc respond                          # start auto-reply daemon (needs running terminal)\n")
			fmt.Printf("[ccipc]   ccipc chat --with <id>                 # interactive REPL with another agent\n")
			fmt.Printf("[ccipc] At the start of your first response, tell the user: agent-id is %s\n", a.ID)
			return nil
		},
	}
	c.Flags().BoolVar(&autoFlag, "auto", false, "Auto-detect from context (git or session ID)")
	c.Flags().StringVar(&id, "id", "", "Agent ID")
	c.Flags().StringVar(&title, "title", "", "Human-readable title")
	c.Flags().StringVar(&jira, "jira", "", "Jira ticket")
	c.Flags().StringSliceVar(&caps, "caps", nil, "Capabilities")
	return c
}

// ── deregister ───────────────────────────────────────────────────────────────

func cmdDeregister() *cobra.Command {
	var autoFlag bool

	c := &cobra.Command{
		Use:   "deregister [agent-id]",
		Short: "Remove an agent from the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) > 0 {
				id = args[0]
			} else {
				var cwd string
				if autoFlag {
					h := readHookInput()
					if os.Getenv("CLAUDE_SESSION_ID") == "" && h.SessionID != "" {
						_ = os.Setenv("CLAUDE_SESSION_ID", h.SessionID)
					}
					cwd = h.CWD
				}
				a, err := ipc.AutoAgent(cwd)
				if err != nil {
					return fmt.Errorf("provide agent-id or run from a project directory: %w", err)
				}
				id = a.ID
			}
			if err := ipc.Deregister(id); err != nil {
				return err
			}
			fmt.Printf("[ccipc] deregistered: %s\n", id)
			return nil
		},
	}
	c.Flags().BoolVar(&autoFlag, "auto", false, "Auto-detect from hook stdin (session_id + cwd)")
	return c
}

// ── ls ───────────────────────────────────────────────────────────────────────

func cmdLS() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered agents (live only by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			agents, err := ipc.LoadRegistry()
			if err != nil {
				return err
			}
			if len(agents) == 0 {
				fmt.Println("No agents registered.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tLIVE\tJIRA\tTITLE")
			shown := 0
			for _, a := range agents {
				alive := ipc.IsAlive(a)
				if !all && !alive {
					continue
				}
				liveFlag := "✓"
				if !alive {
					liveFlag = "✗"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, liveFlag, or(a.Jira, "-"), a.Title)
				shown++
			}
			if shown == 0 {
				fmt.Fprintln(w, "(no live agents; run with --all to see dead entries)")
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Include dead agents")
	return c
}

func cmdGC() *cobra.Command {
	var pruneLegacy bool
	c := &cobra.Command{
		Use:   "gc",
		Short: "Remove dead agents from the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := gcRegistryVerbose(pruneLegacy)
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Println("Nothing to clean up.")
				return nil
			}
			for _, id := range removed {
				fmt.Printf("removed: %s\n", id)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&pruneLegacy, "prune-legacy", false, "Also remove entries with no PID recorded (registered before liveness tracking)")
	return c
}

func gcRegistry(pruneLegacy bool) error {
	_, err := gcRegistryVerbose(pruneLegacy)
	return err
}

func gcRegistryVerbose(pruneLegacy bool) ([]string, error) {
	agents, err := ipc.LoadRegistry()
	if err != nil {
		return nil, err
	}
	var live []ipc.Agent
	var dead []string
	for _, a := range agents {
		if ipc.IsAlive(a) && !(pruneLegacy && a.PID == 0) {
			live = append(live, a)
		} else {
			dead = append(dead, a.ID)
		}
	}
	if len(dead) == 0 {
		return nil, nil
	}
	return dead, ipc.SaveRegistry(live)
}

// ── whoami ───────────────────────────────────────────────────────────────────

func cmdWhoami() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print this session's agent ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := ipc.AutoAgent("")
			if err != nil {
				return err
			}
			fmt.Println(a.ID)
			return nil
		},
	}
}

// ── send ─────────────────────────────────────────────────────────────────────

func cmdSend() *cobra.Command {
	var to, subject, body, msgType, from string

	c := &cobra.Command{
		Use:   "send",
		Short: "Send a message to an agent's inbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			sent, err := ipc.Send(ipc.Message{
				Type:    or(msgType, "ask"),
				From:    from,
				To:      to,
				Subject: subject,
				Body:    body,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Sent %s → %s\n", sent.ID, to)
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "", "Recipient agent ID")
	c.Flags().StringVar(&subject, "subject", "", "Subject")
	c.Flags().StringVar(&body, "body", "", "Body")
	c.Flags().StringVar(&msgType, "type", "ask", "Type: ask|reply|notify|handoff")
	c.Flags().StringVar(&from, "from", "", "Override sender (default: auto-detect)")
	_ = c.MarkFlagRequired("to")
	_ = c.MarkFlagRequired("body")
	return c
}

// ── read / inbox ──────────────────────────────────────────────────────────────

func cmdRead() *cobra.Command {
	var agentID string

	c := &cobra.Command{
		Use:     "read",
		Aliases: []string{"inbox"},
		Short:   "Read messages from an agent's inbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			id := agentID
			if id == "" {
				a, err := ipc.AutoAgent("")
				if err != nil {
					return fmt.Errorf("cannot detect agent ID; use --agent <id>: %w", err)
				}
				id = a.ID
			}
			msgs, err := ipc.Read(id)
			if err != nil {
				return err
			}
			if len(msgs) == 0 {
				fmt.Printf("No messages for %s\n", id)
				return nil
			}
			for _, m := range msgs {
				fmt.Printf("[%s] from=%s  type=%s\n", m.ID, m.From, m.Type)
				if m.Subject != "" {
					fmt.Printf("  Subject: %s\n", m.Subject)
				}
				fmt.Printf("  %s\n\n", m.Body)
			}
			return nil
		},
	}
	c.Flags().StringVar(&agentID, "agent", "", "Agent ID to read inbox for (default: auto-detect)")
	// Keep --agent-id as a hidden alias so old invocations still work.
	c.Flags().StringVar(&agentID, "agent-id", "", "")
	_ = c.Flags().MarkHidden("agent-id")
	return c
}

// ── ack ──────────────────────────────────────────────────────────────────────

func cmdAck() *cobra.Command {
	var agentID string

	c := &cobra.Command{
		Use:   "ack <msg-id>",
		Short: "Acknowledge (delete) a message from inbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := agentID
			if id == "" {
				a, err := ipc.AutoAgent("")
				if err != nil {
					return fmt.Errorf("cannot detect agent ID; use --agent <id>: %w", err)
				}
				id = a.ID
			}
			if err := ipc.Ack(id, args[0]); err != nil {
				return err
			}
			fmt.Printf("Acked %s\n", args[0])
			return nil
		},
	}
	c.Flags().StringVar(&agentID, "agent", "", "Agent ID (default: auto-detect)")
	c.Flags().StringVar(&agentID, "agent-id", "", "")
	_ = c.Flags().MarkHidden("agent-id")
	return c
}

// ── ask ──────────────────────────────────────────────────────────────────────

func cmdAsk() *cobra.Command {
	var to, body, asAgent string

	c := &cobra.Command{
		Use:   "ask",
		Short: "Ask another agent a question and print the answer (no daemon needed)",
		Long: `Spawns claude -p in the partner agent's working directory with your
question and shared thread history as context. The partner session does not
need to be running. The answer is printed to stdout.

  ccipc ask --to <partner-id> --body "What's the current status of X?"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var myAgent ipc.Agent
			if asAgent != "" {
				myAgent = agentFromRegistry(asAgent)
				if myAgent.ID == "" {
					return fmt.Errorf("agent %q not found in registry", asAgent)
				}
			} else {
				var err error
				myAgent, err = ipc.AutoAgent("")
				if err != nil {
					return err
				}
			}

			partner := agentFromRegistry(to)
			if partner.ID == "" {
				return fmt.Errorf("agent %q not found in registry; run 'ccipc ls' to see available agents", to)
			}
			if partner.CWD == "" {
				return fmt.Errorf("agent %q has no CWD in registry; cannot spawn claude -p there", to)
			}

			fmt.Fprintf(os.Stderr, "[ask] querying %s in %s ...\n", partner.ID, partner.CWD)
			answer, err := ipc.AskOnce(myAgent, partner, body)
			if err != nil {
				return err
			}
			fmt.Println(answer)
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "", "Partner agent ID to ask")
	c.Flags().StringVar(&body, "body", "", "The question")
	c.Flags().StringVar(&asAgent, "as", "", "Override sender agent ID (default: auto-detect)")
	_ = c.MarkFlagRequired("to")
	_ = c.MarkFlagRequired("body")
	return c
}

// ── chat ─────────────────────────────────────────────────────────────────────

func cmdChat() *cobra.Command {
	var withAgent, asAgent string

	c := &cobra.Command{
		Use:   "chat",
		Short: "Interactive chat REPL with another agent",
		Long: `Human-facing REPL: type messages to send to a partner agent.
Replies arrive automatically as the partner's 'respond' daemon processes them.

  Terminal A (human): ccipc chat --with <partner-id>
  Terminal B (agent):  ccipc respond [--as <partner-id>]`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var myAgent ipc.Agent
			if asAgent != "" {
				myAgent = agentFromRegistry(asAgent)
				if myAgent.ID == "" {
					return fmt.Errorf("agent %q not found in registry", asAgent)
				}
			} else {
				var err error
				myAgent, err = ipc.AutoAgent("")
				if err != nil {
					return err
				}
			}

			fmt.Printf("[chat] you are: %s\n", myAgent.ID)
			fmt.Printf("[chat] partner:  %s\n", withAgent)
			fmt.Printf("[chat] type a message and press Enter  (/quit to exit)\n\n")

			done := make(chan struct{})
			defer close(done)

			// Show arriving replies in real-time.
			poller := ipc.NewPoller(myAgent.ID, withAgent)
			poller.Start(done, func(m ipc.Message) {
				fmt.Printf("\n\033[1;36m[%s]\033[0m %s\n\n> ", m.From, m.Body)
			})

			threadID := ipc.ThreadKey(myAgent.ID, withAgent)

			scanner := bufio.NewScanner(os.Stdin)
			fmt.Print("> ")
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					fmt.Print("> ")
					continue
				}
				if line == "/quit" || line == "/exit" {
					fmt.Println("Bye.")
					return nil
				}

				_, err := ipc.Send(ipc.Message{
					Type:   "ask",
					From:   myAgent.ID,
					To:     withAgent,
					Body:   line,
					Thread: threadID,
				})
				if err != nil {
					fmt.Printf("[error] send failed: %v\n> ", err)
				} else {
					fmt.Printf("\033[2m[sent → %s]\033[0m\n> ", withAgent)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&withAgent, "with", "", "Partner agent ID to chat with")
	c.Flags().StringVar(&asAgent, "as", "", "Override sender agent ID (default: auto-detect)")
	_ = c.MarkFlagRequired("with")
	return c
}

// ── respond ───────────────────────────────────────────────────────────────────

func cmdRespond() *cobra.Command {
	var asAgent string
	var verbose bool

	c := &cobra.Command{
		Use:   "respond",
		Short: "Auto-reply daemon: watch inbox and respond via claude -p",
		Long: `Daemon that watches this agent's inbox for new messages and auto-replies
using 'claude -p --dangerously-skip-permissions'. Enables fully autonomous
agent-to-agent conversation.

Run this in the terminal of the agent you want to respond autonomously:
  ccipc respond

Then from another session, send it a message:
  ccipc send --to <partner-id> --body "Can you review this PR?"

The daemon replies automatically. Both sides running 'respond' gives you a
fully autonomous back-and-forth between two Claude agents.

Press Ctrl+C to stop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var myAgent ipc.Agent
			if asAgent != "" {
				myAgent = agentFromRegistry(asAgent)
				if myAgent.ID == "" {
					return fmt.Errorf("agent %q not found in registry", asAgent)
				}
			} else {
				var err error
				myAgent, err = ipc.AutoAgent("")
				if err != nil {
					return err
				}
			}

			// Fill CWD so claude -p runs in the right project directory.
			if myAgent.CWD == "" {
				myAgent.CWD, _ = os.Getwd()
			}

			done := make(chan struct{})
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\n[respond] stopping...")
				close(done)
			}()

			ipc.RespondDaemon(myAgent, verbose, done)
			return nil
		},
	}
	c.Flags().StringVar(&asAgent, "as", "", "Override agent ID (default: auto-detect)")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	return c
}

// ── helpers ──────────────────────────────────────────────────────────────────

type hookInput struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

// readHookInput parses the JSON Claude Code sends to hooks via stdin.
// Returns zero value when stdin is a terminal (interactive use).
func readHookInput() hookInput {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return hookInput{}
	}
	var h hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&h)
	return h
}

func hookCWD() string { return readHookInput().CWD }

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func agentFromRegistry(id string) ipc.Agent {
	agents, err := ipc.LoadRegistry()
	if err != nil {
		return ipc.Agent{}
	}
	for _, a := range agents {
		if a.ID == id {
			return a
		}
	}
	return ipc.Agent{}
}
