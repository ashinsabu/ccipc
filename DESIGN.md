# ccipc — Design Document

Last updated after full docs research. Every claim here is verified against
the Claude Code documentation or confirmed experimentally.

---

## What we're building

Local message bus for Claude Code agents on the same machine: discovery,
async messaging, and synchronous query — within the constraints of Bedrock.

---

## Verified constraints (Bedrock / third-party providers)

Do not re-propose any of these. All confirmed against docs or by experiment.

| Mechanism | Status | Source |
|-----------|--------|--------|
| MCP Channels (`claude/channel`) | ✗ BLOCKED | Confirmed experimentally: "Channels are not available on third-party providers" |
| `SendMessage` / `ListAgents` tools | ✗ BLOCKED | Docs: "not available on Amazon Bedrock" |
| `CLAUDE_CODE_MESSAGING_SOCKET` | ✗ NOT BOUND | Docs: socket only binds when cross-session messaging is enabled; Bedrock doesn't enable it |
| `Monitor` tool | ✗ BLOCKED | Docs: "not available on Amazon Bedrock" |
| `ScheduleWakeup` tool | ✗ BLOCKED | Docs: "not available on Amazon Bedrock" |
| `PushNotification` tool | ✗ BLOCKED | Docs: "not accessible from Amazon Bedrock" |
| `RemoteTrigger` / Routines | ✗ BLOCKED | Docs: "not accessible from Amazon Bedrock" |
| `claude -p --resume <id>` | ✗ UNSAFE | Conflicts with the active session holding that ID |
| TIOCSTI / tty write | ✗ WRONG TARGET | Writes to terminal input, not Claude's message queue |
| Push into an idle session | ✗ IMPOSSIBLE | No external injection point exists; session blocks on stdin |

**Root cause of the idle-session constraint:** Claude Code's interactive session
blocks on user input. Nothing external can enqueue into it from outside.

---

## What actually works on Bedrock

Verified from docs:

| Mechanism | Available | Notes |
|-----------|-----------|-------|
| All hooks (SessionStart, Stop, UserPromptSubmit, PostToolUse, asyncRewake) | ✓ | Stable, no provider restrictions |
| `UserPromptSubmit` hook stdout | ✓ | Surfaced as context to Claude before prompt processing |
| `Stop` hook + `asyncRewake: true` | ✓ | Wakes Claude after turn ends, stderr shown as system reminder |
| `CronCreate` / `/loop` with fixed interval | ✓ | Works on Bedrock; no dynamic interval (fixed 10m if omitted), but explicit intervals work |
| `CronDelete`, `CronList` | ✓ | Works |
| `Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep` | ✓ | Always available |
| `claude -p` subprocess (fresh session) | ✓ | Not same session; answers question independently |
| Filesystem (inbox files, registry JSON) | ✓ | Reliable shared state between sessions |
| Regular MCP server tools (non-channel) | ✓ | Tools exposed via MCP work normally; only channels are blocked |

---

## Delivery model

The hard constraint: **no mechanism to push into an idle session on Bedrock**.
Best achievable: self-scheduled polling via `CronCreate`.

### Three layers in order of recency

**Layer 1: Session start (SessionStart hook)**
- Hook registers agent, prints inbox status + command ref
- Instructions tell Claude: schedule `ccipc inbox` check via CronCreate
- Claude calls CronCreate immediately: `*/2 * * * *` → `ccipc inbox --check`
- This becomes a recurring task restored on `--resume`

**Layer 2: Self-scheduled polling (CronCreate)**
- Every 2 minutes, while idle, Claude wakes itself up
- Runs `ccipc inbox`, processes pending messages
- Replies via `ccipc send` back to sender
- On Bedrock: fixed schedule only, but 2-minute interval is fine

**Layer 3: Turn-start injection (UserPromptSubmit hook)**
- On every user turn, hook checks inbox
- If messages found: printed to stdout → Claude sees them before user prompt
- Catches anything missed while session was idle with no cron fires

Together: messages delivered within ~2 minutes when session is active (cron),
or at next user interaction when session was truly idle (UserPromptSubmit).

### asyncRewake (Stop hook) — optional fourth layer
- Fires when Claude finishes a turn
- If inbox non-empty: exits 2, `asyncRewake: true` wakes Claude immediately
- Delivers messages that arrived right as Claude finished a response
- Lower value than CronCreate since cron already covers this within 2 min

---

## Architecture

```
~/.claude/ipc/
  registry.json          # live agents: ID, CWD, PID, Beat, branch
  inbox/
    <agent-id>/
      <ts>-<msg-id>.json # one file per message; deleted on ack
  threads/
    <key>.json           # thread history for ask/respond exchanges (capped at 30 turns)
```

---

## Components

### `ccipc register --auto` (SessionStart hook)
- Derives agent ID deterministically from CWD + `CLAUDE_CODE_SESSION_ID`
- Writes entry to registry with PID + Beat timestamp
- Prunes dead entries (PID check + 1hr Beat TTL)
- Prints to stdout (Claude sees this as context):
  - Agent ID
  - Inbox status
  - Full command reference
  - **Instruction: "Schedule inbox polling: use CronCreate with `*/2 * * * *` and prompt `ccipc inbox --check`. Do this now."**

### `ccipc deregister --auto` (Stop hook)
- Removes own entry from registry
- Optionally paired with asyncRewake to deliver any last messages before exiting

### `ccipc ls`
- Shows live agents (PID alive OR Beat < 1hr)
- `--all` includes dead entries

### `ccipc send --to <id> --body "..."`
- Fire-and-forget: drops JSON to target inbox
- Exits immediately

### `ccipc inbox [--check]`
- Reads own inbox
- `--check`: silent if empty (for cron use); only prints if messages found
- Claude processes messages and replies with `ccipc send`

### `ccipc ack <msg-id>`
- Deletes message from inbox after processing

### `ccipc ask --to <id> --body "..."`
- Spawns `claude -p --dangerously-skip-permissions` in partner's CWD
- Uses thread history as context
- Blocks, prints answer
- No cron/daemon needed on partner side; always works
- Tradeoff: fresh session, not the live one

### `ccipc respond` (daemon, run in terminal)
- Polls inbox every 2s
- Processes each message via `claude -p` in own CWD
- Replies to sender
- Enables autonomous back-and-forth
- Complement to CronCreate (respond = faster, more autonomous; cron = no terminal needed)

### `ccipc chat --with <id>`
- Interactive REPL for human ↔ agent conversation
- Polls own inbox for replies from partner
- Partner runs `ccipc respond` in their terminal

### `ccipc gc [--prune-legacy]`
- Removes dead agents from registry
- `--prune-legacy`: removes PID=0 entries (pre-liveness-tracking)

---

## Agent ID stability

Same session → same ID across register/deregister cycles.

Format: `<repo-base>-<adjective>-<noun>`
Seed: first 8 chars of `CLAUDE_CODE_SESSION_ID` (fallback: `CLAUDE_SESSION_ID`)
Hash: FNV-32 over seed → deterministic word pair

Example: `gitops-agent-dense-anvil`

---

## Liveness

```go
func IsAlive(a Agent) bool {
    if pidAlive(a.PID) { return true }             // process alive
    if a.Beat == "" { return a.PID == 0 }           // legacy compat
    t, _ := time.Parse(time.RFC3339, a.Beat)
    return time.Since(t) < time.Hour               // Beat < 1hr
}
```

Beat refreshed on every `ccipc` invocation via `PersistentPreRun`.

---

## Hook wiring (`~/.claude/settings.json`)

```json
{
  "hooks": {
    "SessionStart": [{"type": "command", "command": "ccipc register --auto"}],
    "Stop": [{"type": "command", "command": "ccipc deregister --auto"}],
    "UserPromptSubmit": [{"type": "command", "command": "ccipc _prompt-hook"}],
    "Stop": [{
      "type": "command",
      "command": "ccipc _wake-hook",
      "async": true,
      "asyncRewake": true
    }]
  }
}
```

Note: two Stop hooks — deregister fires on session end; wake hook fires on every
turn end while session is active. Claude Code supports multiple hooks per event.

---

## MCP server option (future / optional)

A regular (non-channel) MCP server could expose ccipc operations as native tools:
- `ccipc_list_agents` — reads registry, returns live agents
- `ccipc_send` — drops to inbox
- `ccipc_read_inbox` — reads own inbox

Advantage: Claude would treat these as first-class tools, not bash invocations.
No permission prompts since they'd be in an MCP server.
Disadvantage: Go MCP server adds complexity; current CLI + Bash is simpler and works.

Defer until the file-based CLI approach hits a real friction point.

---

## What ccipc does NOT solve

These are platform constraints, not design gaps:

- **Push into a truly idle session**: impossible on Bedrock. The 2-minute CronCreate
  poll is the best approximation.
- **Real-time interruption of an active task**: cron fires only when Claude is idle.
- **Delivery confirmation**: sender cannot know when partner processed the message.
- **Native session-to-session messaging**: `SendMessage`/`ListAgents` are blocked on
  Bedrock. File-based inbox is the workaround.

---

## Comparison: what first-party providers get vs Bedrock

| Feature | claude.ai / API | Bedrock |
|---------|----------------|---------|
| `SendMessage` to local sessions | ✓ direct, real-time | ✗ blocked |
| MCP Channels | ✓ | ✗ blocked |
| `Monitor` tool | ✓ | ✗ blocked |
| `ScheduleWakeup` (self-paced loop) | ✓ | ✗ blocked |
| `CronCreate` (fixed interval) | ✓ | ✓ |
| File-based inbox + hooks | ✓ | ✓ |
| `claude -p` subprocess | ✓ | ✓ |

ccipc targets Bedrock-compatible mechanisms only.
