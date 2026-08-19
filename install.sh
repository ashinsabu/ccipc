#!/usr/bin/env sh
set -e

REPO="ashinsabu/ccipc"
BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/ccipc"
SETTINGS="${HOME}/.claude/settings.json"

# ── detect platform ───────────────────────────────────────────────────────────

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac
case "${OS}" in
  darwin|linux) ;;
  *) echo "Unsupported OS: ${OS}" >&2; exit 1 ;;
esac

ASSET="ccipc-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

# ── preview changes ───────────────────────────────────────────────────────────

echo "ccipc installer"
echo "==============="
echo ""
echo "Changes this script will make:"
echo ""

# 1. Binary
if [ -f "${BIN}" ]; then
  CURRENT_VER="$("${BIN}" --version 2>/dev/null | head -1 || echo "unknown")"
  echo "  [~] Binary already at ${BIN} (${CURRENT_VER}) — will update to latest"
else
  echo "  [+] Download binary → ${BIN}"
fi

# 2. PATH in shell profile
PROFILE=""
NEEDS_PATH=false
if ! command -v ccipc >/dev/null 2>&1 && ! echo "${PATH}" | grep -q "${BIN_DIR}"; then
  for f in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.profile"; do
    if [ -f "${f}" ]; then
      PROFILE="${f}"
      break
    fi
  done
  if [ -n "${PROFILE}" ]; then
    if ! grep -qF "${BIN_DIR}" "${PROFILE}"; then
      echo "  [+] Add to ${PROFILE}: export PATH=\"\$PATH:${BIN_DIR}\""
      NEEDS_PATH=true
    else
      echo "  [=] ${BIN_DIR} already in ${PROFILE} — no change"
    fi
  else
    echo "  [!] No shell profile found — add ${BIN_DIR} to PATH manually"
  fi
fi

# 3. Hooks
HOOK_SESSION_PRESENT=false
HOOK_STOP_PRESENT=false
if [ -f "${SETTINGS}" ]; then
  grep -q "ccipc register" "${SETTINGS}" 2>/dev/null && HOOK_SESSION_PRESENT=true
  grep -q "ccipc deregister" "${SETTINGS}" 2>/dev/null && HOOK_STOP_PRESENT=true
fi
if ${HOOK_SESSION_PRESENT} && ${HOOK_STOP_PRESENT}; then
  echo "  [=] ~/.claude/settings.json hooks already present — no change"
else
  [ -f "${SETTINGS}" ] && \
    echo "  [~] Patch ~/.claude/settings.json (existing content preserved):" || \
    echo "  [+] Create ~/.claude/settings.json:"
  ${HOOK_SESSION_PRESENT} || echo "        + hooks.SessionStart: ccipc register --auto"
  ${HOOK_STOP_PRESENT}    || echo "        + hooks.Stop:         ccipc deregister --auto"
fi

echo ""
printf "Proceed? [y/N] "
read -r answer
case "${answer}" in
  [Yy]|[Yy][Ee][Ss]) ;;
  *) echo "Aborted."; exit 0 ;;
esac

# ── install ───────────────────────────────────────────────────────────────────

echo ""
mkdir -p "${BIN_DIR}"

echo "Downloading ccipc (${OS}/${ARCH})..."
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${URL}" -o "${BIN}.tmp"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${BIN}.tmp" "${URL}"
else
  echo "Neither curl nor wget found." >&2; exit 1
fi
chmod +x "${BIN}.tmp"
mv "${BIN}.tmp" "${BIN}"
echo "  → ${BIN}"

# PATH (only if not already present in profile)
if ${NEEDS_PATH} && [ -n "${PROFILE}" ]; then
  if ! grep -qF "${BIN_DIR}" "${PROFILE}"; then
    printf '\nexport PATH="$PATH:%s"\n' "${BIN_DIR}" >> "${PROFILE}"
    echo "  → patched ${PROFILE}"
  fi
  export PATH="${PATH}:${BIN_DIR}"
fi

# Hooks (ccipc install is itself idempotent — skips if already present)
"${BIN}" install

echo ""
echo "Done. ccipc will auto-register/deregister on every Claude Code session."
echo "To verify: open Claude Code and run: ccipc ls"
