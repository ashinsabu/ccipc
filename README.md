# ccipc

Local message bus for Claude Code agents. Lets Claude sessions on the same machine discover each other, send messages, and ask questions — with no daemon required on the receiving end.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ashinsabu/ccipc/main/install.sh | sh
```

This downloads the binary, places it in `~/.local/bin/ccipc`, and wires two hooks into `~/.claude/settings.json`:

- **SessionStart** — registers your agent in the local registry
- **Stop** — deregisters it when the session ends

After that, every Claude Code session gets an agent ID automatically. The installer shows you exactly what it will change and asks for confirmation before touching anything.

## Commands

```bash
ccipc ls                                # list live agents on this machine
ccipc inbox                             # read your messages
ccipc ack <msg-id>                      # delete a message after reading
ccipc send --to <id> --body "..."       # send async message (fire and forget)
ccipc ask  --to <id> --body "..."       # ask a question and get the answer now
ccipc respond                           # start auto-reply daemon
ccipc chat --with <id>                  # interactive REPL with another agent
ccipc whoami                            # print your agent ID
ccipc gc                                # remove dead agents from registry
ccipc gc --prune-legacy                 # also remove entries with no PID recorded
```

## How it works

Each Claude Code session registers itself in `~/.claude/ipc/registry.json` with its ID, working directory, and PID. Messages are JSON files dropped into per-agent inbox directories under `~/.claude/ipc/inboxes/`.

Agent IDs are deterministic — same session always gets the same name (e.g. `gitops-agent-floating-moon`) — so you can reference them reliably across commands.

`ccipc ls` filters by liveness: it checks whether each agent's recorded PID is still running before showing it. Dead entries are hidden by default and pruned by `ccipc gc`.

### `ccipc ask` vs `ccipc send`

| | `send` | `ask` |
|---|---|---|
| Partner needs to be running | No | No |
| Blocking | No | Yes |
| Gets a reply | Only if partner runs `respond` | Always |

`ccipc ask` spawns `claude -p` in the partner's working directory with your shared conversation history as context. No daemon, no waiting — you get the answer inline.

### `ccipc respond`

Run this in your terminal to become an auto-reply agent. It watches your inbox and replies to each message by running `claude -p` in your project directory. Both sides running `respond` gives you fully autonomous agent-to-agent conversation.

## Why this exists

Claude Code has a native `SendMessage` / `ListAgents` toolset for cross-session messaging, but it is not available on all providers. ccipc is a file-based drop-in that works everywhere.

## Requirements

- macOS or Linux
- Claude Code CLI installed and on PATH
- Go 1.24+ (only needed if building from source)
