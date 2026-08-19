package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Agent struct {
	ID      string   `json:"id"`
	Owner   string   `json:"owner,omitempty"`
	Title   string   `json:"title"`
	Repo    string   `json:"repo"`
	Branch  string   `json:"branch"`
	Jira    string   `json:"jira,omitempty"`
	Caps    []string `json:"caps"`
	Session string   `json:"session,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	PID     int      `json:"pid,omitempty"` // CLAUDE_PID at register time — used for liveness check
	Status  string   `json:"status"`
	Since   string   `json:"since"`
	Beat    string   `json:"beat"`
	V       int      `json:"v"`
}

const beatTTL = 1 * time.Hour

// IsAlive returns true if the agent is considered live.
// An agent is live if its PID is still running OR its Beat timestamp is recent
// (within beatTTL). The Beat fallback handles cases where Claude Code restarts
// its process internally (model switch, /clear, auto-update) while the session
// continues — the PID changes but the session is still active.
func IsAlive(a Agent) bool {
	if pidAlive(a.PID) {
		return true
	}
	if a.Beat == "" {
		return a.PID == 0 // legacy entry with no beat: keep it
	}
	t, err := time.Parse(time.RFC3339, a.Beat)
	if err != nil {
		return false
	}
	return time.Since(t) < beatTTL
}

func pidAlive(pid int) bool {
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TouchBeat updates the Beat timestamp for an agent in the registry.
func TouchBeat(id string) {
	agents, err := LoadRegistry()
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, a := range agents {
		if a.ID == id {
			agents[i].Beat = now
			_ = saveRegistry(agents)
			return
		}
	}
}

func registryPath() string {
	return filepath.Join(ipcDir(), "registry.json")
}

func LoadRegistry() ([]Agent, error) {
	data, err := os.ReadFile(registryPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var agents []Agent
	return agents, json.Unmarshal(data, &agents)
}

func SaveRegistry(agents []Agent) error { return saveRegistry(agents) }

func saveRegistry(agents []Agent) error {
	if err := os.MkdirAll(ipcDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(), data, 0644)
}

func Register(a Agent) error {
	agents, err := LoadRegistry()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.V = 1
	a.Status = "active"
	a.Beat = now
	found := false
	for i, existing := range agents {
		if existing.ID == a.ID {
			a.Since = existing.Since
			agents[i] = a
			found = true
			break
		}
	}
	if !found {
		a.Since = now
		agents = append(agents, a)
	}
	if err := os.MkdirAll(InboxPath(a.ID), 0755); err != nil {
		return err
	}
	return saveRegistry(agents)
}

func Deregister(id string) error {
	agents, err := LoadRegistry()
	if err != nil {
		return err
	}
	filtered := agents[:0]
	for _, a := range agents {
		if a.ID != id {
			filtered = append(filtered, a)
		}
	}
	return saveRegistry(filtered)
}

// AutoAgent builds an Agent from context in dir (or current directory if empty).
// Works in both git repos and plain directories.
func AutoAgent(dir string) (Agent, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			dir = "."
		}
	}

	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}

	session := os.Getenv("CLAUDE_SESSION_ID")
	if session == "" {
		session = os.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	sessionShort := session
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}

	var branch, repo, jira string

	// Try git — may fail in non-repo dirs, that's fine.
	if b, err := gitIn(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(b)
		if origin, err := gitIn(dir, "remote", "get-url", "origin"); err == nil {
			repo = repoFromRemote(strings.TrimSpace(origin))
		}
	}

	// Fallback for non-git directories.
	if branch == "" {
		if sessionShort != "" {
			branch = "claude-" + sessionShort
		} else {
			branch = "unknown"
		}
	}
	if repo == "" {
		repo = filepath.Base(dir)
	}

	if m := regexp.MustCompile(`[A-Z]+-\d+`).FindString(branch); m != "" {
		jira = m
	}

	repoBase := filepath.Base(repo)
	id := fmt.Sprintf("%s-%s", repoBase, randomSuffix(sessionShort))

	pid, _ := strconv.Atoi(os.Getenv("CLAUDE_PID"))

	return Agent{
		ID:      id,
		Title:   fmt.Sprintf("%s — %s", repoBase, branch),
		Repo:    repo,
		Branch:  branch,
		Jira:    jira,
		Session: sessionShort,
		CWD:     dir,
		PID:     pid,
		Caps:    []string{},
	}, nil
}

// randomSuffix picks two words deterministically from the session ID so the
// same session always gets the same name (stable across register calls).
func randomSuffix(seed string) string {
	h := fnv32(seed)
	a := adjectives[h%uint32(len(adjectives))]
	n := nouns[(h/uint32(len(adjectives)))%uint32(len(nouns))]
	return a + "-" + n
}

func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

var adjectives = []string{
	"ancient", "autumn", "azure", "bitter", "blazing", "blunt", "bold", "brave",
	"bright", "broken", "calm", "cold", "cool", "cosmic", "crimson", "cryptic",
	"damp", "dark", "dawn", "deft", "dense", "dry", "dusk", "dusty",
	"eager", "echo", "empty", "epic", "eternal", "fallen", "feral", "fierce",
	"fleet", "floating", "fog", "frosty", "frozen", "furious", "gentle", "giant",
	"glacial", "glowing", "grand", "grave", "green", "grim", "hidden", "hollow",
	"humble", "icy", "idle", "iron", "jade", "keen", "kind", "late",
	"lean", "light", "lone", "long", "loud", "lucky", "lunar", "mild",
	"mist", "murky", "mute", "narrow", "neat", "noble", "numb", "odd",
	"old", "open", "outer", "pale", "patient", "plain", "proud", "pure",
	"quick", "quiet", "rapid", "rare", "raw", "red", "rogue", "rough",
	"royal", "rugged", "rustic", "savage", "scarlet", "serene", "sharp", "silent",
	"silver", "sleek", "slim", "slow", "small", "smart", "smooth", "soft",
	"solar", "solid", "somber", "sonic", "stark", "steady", "stern", "still",
	"stone", "stormy", "stray", "strong", "swift", "tall", "tame", "thin",
	"thorn", "tiny", "tired", "true", "twilight", "vast", "velvet", "violet",
	"warm", "wild", "wily", "wise", "wooden", "young", "zealous", "zenith",
}

var nouns = []string{
	"abyss", "anchor", "anvil", "arc", "ash", "atlas", "atom", "axe",
	"basin", "bay", "beacon", "bear", "blade", "bolt", "branch", "bridge",
	"brook", "canyon", "cedar", "chain", "cipher", "circuit", "cliff", "cloud",
	"comet", "core", "crane", "crest", "crown", "current", "dale", "dawn",
	"delta", "den", "depth", "drift", "drum", "dune", "dust", "echo",
	"edge", "ember", "epoch", "fang", "field", "flare", "flame", "fleet",
	"flint", "flux", "forge", "fork", "frost", "gate", "glacier", "glyph",
	"grove", "gulf", "harbor", "hawk", "helm", "hill", "horizon", "hull",
	"iris", "isle", "jolt", "keystone", "lance", "lantern", "lattice", "leaf",
	"lens", "lever", "light", "loch", "lodge", "loop", "lynx", "maze",
	"mesa", "mist", "moon", "moth", "mount", "nexus", "node", "north",
	"nova", "oak", "orbit", "otter", "petal", "pine", "plain", "prism",
	"pulse", "quartz", "quest", "raven", "reef", "relay", "ridge", "rift",
	"ring", "river", "rock", "root", "rune", "sail", "sand", "seal",
	"shard", "shore", "signal", "siren", "slate", "slope", "smoke", "snow",
	"spark", "spire", "spring", "star", "stem", "stone", "storm", "strand",
	"stream", "summit", "surge", "swarm", "swift", "tide", "timber", "torch",
	"tower", "trail", "tundra", "vale", "vault", "veil", "vortex", "wave",
	"web", "well", "wind", "wolf", "wood", "wraith", "yarn", "zenith",
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	return string(out), err
}

func repoFromRemote(remote string) string {
	// https://github.com/org/repo.git  →  org/repo
	// git@github.com:org/repo.git      →  org/repo
	// https://git0.harness.io/.../org/repo.git  →  org/repo
	remote = strings.TrimSuffix(remote, ".git")
	// For SSH git@host:org/repo, extract after the colon.
	if i := strings.Index(remote, ":"); i >= 0 && !strings.Contains(remote[:i], "/") {
		remote = remote[i+1:]
	}
	// Take last two path segments as org/repo.
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	if len(parts) == 1 && parts[0] != "" {
		return parts[0]
	}
	return ""
}
