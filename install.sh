#!/bin/sh
set -e

REPO="kiiimatz/tailway"

# ── Detect OS ─────────────────────────────────────────────────────────────────
OS="$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)  OS="linux"  ;;
  darwin*) OS="darwin" ;;
  *)
    echo "Unsupported OS: $OS"
    echo "On Windows, use install.ps1 instead:"
    echo "  irm https://raw.githubusercontent.com/kiiimatz/tailway/main/install.ps1 | iex"
    exit 1
    ;;
esac

# ── Detect arch ───────────────────────────────────────────────────────────────
ARCH="$(uname -m 2>/dev/null)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# ── Fetch latest release tag ──────────────────────────────────────────────────
echo "Fetching latest release..."
API_URL="https://api.github.com/repos/$REPO/releases/latest"

if command -v curl >/dev/null 2>&1; then
  VERSION="$(curl -fsSL "$API_URL" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
elif command -v wget >/dev/null 2>&1; then
  VERSION="$(wget -qO- "$API_URL" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
else
  echo "curl or wget is required."
  exit 1
fi

if [ -z "$VERSION" ]; then
  echo "Could not determine latest version. Check your internet connection."
  exit 1
fi

echo "Latest version: $VERSION"

# ── Download ──────────────────────────────────────────────────────────────────
ASSET="tailway-$OS-$ARCH"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET"
TMP_FILE="$(mktemp /tmp/tailway-XXXXXX)"

echo "Downloading $ASSET..."
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_FILE"
else
  wget -qO "$TMP_FILE" "$DOWNLOAD_URL"
fi
chmod +x "$TMP_FILE"

# ── Install ───────────────────────────────────────────────────────────────────
# Try /usr/local/bin first (already in PATH everywhere).
# Fall back to ~/.local/bin and patch shell configs automatically.
if [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
  mv "$TMP_FILE" "$INSTALL_DIR/tailway"
  NEED_PATH=0
elif command -v sudo >/dev/null 2>&1; then
  INSTALL_DIR="/usr/local/bin"
  sudo mv "$TMP_FILE" "$INSTALL_DIR/tailway"
  NEED_PATH=0
else
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
  mv "$TMP_FILE" "$INSTALL_DIR/tailway"
  NEED_PATH=1
fi

echo ""
echo "Installed tailway $VERSION to $INSTALL_DIR/tailway"

# ── Auto-add to PATH if needed ────────────────────────────────────────────────
add_to_path() {
  PROFILE="$1"
  LINE="export PATH=\"\$HOME/.local/bin:\$PATH\""

  # Skip if already present
  if [ -f "$PROFILE" ] && grep -qF '.local/bin' "$PROFILE" 2>/dev/null; then
    return
  fi

  printf '\n# Added by tailway installer\n%s\n' "$LINE" >> "$PROFILE"
  echo "Added PATH entry to $PROFILE"
}

if [ "$NEED_PATH" = "1" ]; then
  # Check current session first
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) NEED_PATH=0 ;;
  esac
fi

if [ "$NEED_PATH" = "1" ]; then
  # Patch every shell config that exists
  add_to_path "$HOME/.bashrc"
  add_to_path "$HOME/.zshrc"

  # macOS: also patch login shell profiles
  if [ "$OS" = "darwin" ]; then
    add_to_path "$HOME/.bash_profile"
    add_to_path "$HOME/.zprofile"
  fi

  # Apply to current session so the command works right now
  export PATH="$INSTALL_DIR:$PATH"
fi

echo ""
echo "Run: tailway"
