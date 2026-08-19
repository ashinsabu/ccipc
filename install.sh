#!/usr/bin/env sh
set -e

REPO="ashinsabu/ccipc"
BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/ccipc"
SETTINGS="${HOME}/.claude/settings.json"

# Detect OS and arch
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH}" >&2
    exit 1
    ;;
esac

case "${OS}" in
  darwin|linux) ;;
  *)
    echo "Unsupported OS: ${OS}" >&2
    exit 1
    ;;
esac

ASSET="ccipc-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "ccipc installer"
echo "==============="
echo ""
echo "This script will make the following changes:"
echo ""
echo "  1. Download binary → ${BIN}"
echo "  2. Add to ~/.claude/settings.json:"
echo "       hooks.SessionStart: ccipc register --auto  (auto-register on session start)"
echo "       hooks.Stop:         ccipc deregister --auto  (auto-deregister on session end)"
echo ""

if [ -f "${BIN}" ]; then
  echo "  Note: ${BIN} already exists and will be overwritten."
fi
if [ -f "${SETTINGS}" ]; then
  echo "  Note: ${SETTINGS} exists and will be patched (existing content preserved)."
fi

echo ""
printf "Proceed? [y/N] "
read -r answer
case "${answer}" in
  [Yy]|[Yy][Ee][Ss]) ;;
  *)
    echo "Aborted."
    exit 0
    ;;
esac

echo ""
echo "Downloading ccipc (${OS}/${ARCH})..."
mkdir -p "${BIN_DIR}"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${URL}" -o "${BIN}"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${BIN}" "${URL}"
else
  echo "Neither curl nor wget found. Install one and retry." >&2
  exit 1
fi

chmod +x "${BIN}"
echo "Installed → ${BIN}"

# Ensure ~/.local/bin is on PATH
if ! echo "${PATH}" | grep -q "${BIN_DIR}"; then
  echo ""
  echo "~/.local/bin is not in your PATH. Add this to your shell profile (~/.zshrc or ~/.bashrc):"
  echo "  export PATH=\"\$PATH:${BIN_DIR}\""
  export PATH="${PATH}:${BIN_DIR}"
fi

# Wire hooks into ~/.claude/settings.json
"${BIN}" install

echo ""
echo "Done. ccipc will register/deregister automatically on every Claude Code session."
echo "To verify: open Claude Code and run: ccipc ls"
