#!/usr/bin/env bash
# NeoExit installer for Ubuntu / Debian.
#
#   curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neoexit/main/install.sh | sudo bash
#
# Options (after "bash -s --" when piping):
#   --version vX.Y.Z  install a specific release (default: latest)
#   --binary PATH     install from a binary you already downloaded
#   --upgrade         upgrade without opening the menu
#   --uninstall       remove NeoExit and its settings
#
# Run again in a terminal on a server that has NeoExit, it opens the menu
# (update, new password, uninstall...); otherwise it installs or upgrades,
# keeping the settings and password.
set -euo pipefail

REPO="neoauroraproject/neoexit"
VERSION="latest" BINARY="" UNINSTALL=0 MENU=1

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; MENU=0; shift ;;
    --binary) BINARY="$2"; MENU=0; shift ;;
    --upgrade) MENU=0 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help) sed -n '2,15p' "$0" 2>/dev/null || true; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

c_ok=$'\033[38;5;78m' c_dim=$'\033[2m' c_acc=$'\033[38;5;36m' c_err=$'\033[38;5;203m' c_0=$'\033[0m'
step() { printf '%s›%s %s\n' "$c_acc" "$c_0" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_ok" "$c_0" "$*"; }
die()  { printf '%s✗ %s%s\n' "$c_err" "$*" "$c_0" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root: curl -fsSL ... | sudo bash"
[ "$(uname -s)" = "Linux" ] || die "NeoExit runs on Linux"

if [ "$UNINSTALL" = 1 ]; then
  systemctl disable --now neoexit 2>/dev/null || true
  rm -f /etc/systemd/system/neoexit.service /usr/local/bin/neoexit
  rm -rf /etc/neoexit /run/neoexit
  systemctl daemon-reload
  ok "NeoExit removed"
  exit 0
fi

# Already installed and someone at the keyboard: the menu. Not from
# NeoTunnel or a script (no terminal), and only if this NeoExit has one.
if [ "$MENU" = 1 ] && [ -x /usr/local/bin/neoexit ] && [ -t 1 ] && { : </dev/tty; } 2>/dev/null; then
  help=$(/usr/local/bin/neoexit </dev/null 2>&1 || true)
  case "$help" in *$'\n  menu '*) exec /usr/local/bin/neoexit menu </dev/tty ;; esac
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m) (amd64 and arm64 only)" ;;
esac
NAME="neoexit-linux-$ARCH"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

printf '\n%s◆ NeoExit%s %sinstaller%s\n\n' "$c_acc" "$c_0" "$c_dim" "$c_0"

if [ -n "$BINARY" ]; then
  step "using local binary $BINARY"
  cp "$BINARY" "$TMP/$NAME"
else
  if [ "$VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
  else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  fi
  command -v curl >/dev/null || { apt-get update -qq && apt-get install -y -qq curl >/dev/null; }
  step "downloading $NAME ($VERSION)"
  curl -fL --retry 2 --connect-timeout 15 --progress-bar "$BASE/$NAME" -o "$TMP/$NAME" || die "download failed: $BASE/$NAME"
  if curl -fsSL "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS" 2>/dev/null; then
    (cd "$TMP" && grep " $NAME\$" SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch"
    ok "checksum verified"
  fi
fi

chmod 0755 "$TMP/$NAME"
# 1.4.0 and later set themselves up ("neoexit install"); older ones below.
help=$("$TMP/$NAME" </dev/null 2>&1 || true)
if printf '%s\n' "$help" | grep -q '^  install '; then
  "$TMP/$NAME" install || die "install failed"
  [ -x /usr/local/bin/neotunnel ] && printf '%sNeoTunnel found: its panel manages NeoExit on this server.%s\n\n' "$c_dim" "$c_0"
  exit 0
fi

install -m 0755 "$TMP/$NAME" /usr/local/bin/neoexit
ok "installed $(/usr/local/bin/neoexit version)"

FIRST=0
if [ ! -f /etc/neoexit/config.json ]; then
  FIRST=1
  /usr/local/bin/neoexit init > "$TMP/login.txt"
fi

cat > /etc/systemd/system/neoexit.service <<'EOF'
[Unit]
Description=NeoExit: free exits for AI services
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/neoexit run
Restart=always
RestartSec=3
LimitNOFILE=1048576
RuntimeDirectory=neoexit
RuntimeDirectoryMode=0755

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable neoexit >/dev/null 2>&1
systemctl restart neoexit
ok "service running"

if [ "$FIRST" = 1 ]; then
  cat "$TMP/login.txt"
  printf '%sThe password is shown only now. Forgot it? Run: neoexit password%s\n\n' "$c_dim" "$c_0"
else
  /usr/local/bin/neoexit info
  printf '%sSettings and password kept.%s\n\n' "$c_dim" "$c_0"
fi
[ -x /usr/local/bin/neotunnel ] && printf '%sNeoTunnel found: its panel shows NeoExit on this server.%s\n\n' "$c_dim" "$c_0"
exit 0
