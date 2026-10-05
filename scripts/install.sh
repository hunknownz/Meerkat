#!/bin/sh
set -eu
# Bootstrap prerequisites privately. The Node installer owns the remaining
# installation steps; the Go service owns all task/process scheduling.
umask 077
MEERKAT_SOURCE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEERKAT_OS=$(uname -s)
case "$MEERKAT_OS" in Darwin) MEERKAT_TARGET=darwin;; Linux) MEERKAT_TARGET=linux;; *) echo 'Use scripts/install.ps1 on native Windows' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) MEERKAT_ARCH=arm64;; x86_64) MEERKAT_ARCH=amd64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
MEERKAT_TOOLS_DIR="$HOME/.meerkat/tools"
private_dir() {
  if [ -L "$1" ]; then echo 'Private directory is a symlink' >&2; exit 1; fi
  if [ -e "$1" ]; then
    if [ "$MEERKAT_OS" = Darwin ]; then MEERKAT_PERMS=$(stat -f '%OLp' "$1"); else MEERKAT_PERMS=$(stat -c '%a' "$1"); fi
    if [ ! -d "$1" ] || [ ! -O "$1" ] || [ "$MEERKAT_PERMS" != 700 ]; then echo 'Existing directory must be private and owned by you' >&2; exit 1; fi
  else mkdir "$1"; fi
}
checksum() { if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }
MEERKAT_NODE=$(command -v node || true)
if [ -z "$MEERKAT_NODE" ] || ! "$MEERKAT_NODE" -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||a===22&&b>=19?0:1)'; then
  private_dir "$HOME/.meerkat"; private_dir "$MEERKAT_TOOLS_DIR"
  MEERKAT_NODE_ARCH=$MEERKAT_ARCH; if [ "$MEERKAT_ARCH" = amd64 ]; then MEERKAT_NODE_ARCH=x64; fi
  MEERKAT_SUMS=$(curl -fsSL --max-time 60 https://nodejs.org/dist/latest-v22.x/SHASUMS256.txt)
  MEERKAT_LINE=$(printf '%s\n' "$MEERKAT_SUMS" | awk -v suffix="-$MEERKAT_TARGET-$MEERKAT_NODE_ARCH.tar.gz" 'index($2,suffix) && substr($2,length($2)-length(suffix)+1)==suffix {print;exit}')
  MEERKAT_HASH=$(printf '%s\n' "$MEERKAT_LINE" | awk '{print $1}'); MEERKAT_FILE=$(printf '%s\n' "$MEERKAT_LINE" | awk '{print $2}')
  case "$MEERKAT_FILE" in node-v22.*-*.tar.gz) ;; *) echo 'Node checksum unavailable' >&2; exit 1;; esac
  MEERKAT_ARCHIVE="$MEERKAT_TOOLS_DIR/$MEERKAT_FILE"
  curl -fsSL --max-time 600 "https://nodejs.org/dist/latest-v22.x/$MEERKAT_FILE" -o "$MEERKAT_ARCHIVE"
  [ "$(checksum "$MEERKAT_ARCHIVE")" = "$MEERKAT_HASH" ] || { rm -f "$MEERKAT_ARCHIVE"; echo 'Node checksum mismatch' >&2; exit 1; }
  tar -xzf "$MEERKAT_ARCHIVE" -C "$MEERKAT_TOOLS_DIR"; rm -f "$MEERKAT_ARCHIVE"
  MEERKAT_NODE="$MEERKAT_TOOLS_DIR/${MEERKAT_FILE%.tar.gz}/bin/node"
fi
if ! command -v go >/dev/null 2>&1; then
  private_dir "$HOME/.meerkat"; private_dir "$MEERKAT_TOOLS_DIR"
  MEERKAT_META=$(curl -fsSL --max-time 60 'https://go.dev/dl/?mode=json&include=all')
  MEERKAT_GO_INFO=$(printf '%s' "$MEERKAT_META" | "$MEERKAT_NODE" --input-type=module -e 'let s="";for await(const c of process.stdin)s+=c;const r=JSON.parse(s).find(r=>r.version==="go1.26.0");const f=r?.files.find(f=>f.os===process.argv[1]&&f.arch===process.argv[2]&&f.kind==="archive");if(!f)throw Error("Go download unavailable");console.log(f.filename+" "+f.sha256)' "$MEERKAT_TARGET" "$MEERKAT_ARCH")
  MEERKAT_FILE=${MEERKAT_GO_INFO% *}; MEERKAT_HASH=${MEERKAT_GO_INFO#* }; MEERKAT_ARCHIVE="$MEERKAT_TOOLS_DIR/$MEERKAT_FILE"
  curl -fsSL --max-time 600 "https://go.dev/dl/$MEERKAT_FILE" -o "$MEERKAT_ARCHIVE"
  [ "$(checksum "$MEERKAT_ARCHIVE")" = "$MEERKAT_HASH" ] || { rm -f "$MEERKAT_ARCHIVE"; echo 'Go checksum mismatch' >&2; exit 1; }
  private_dir "$MEERKAT_TOOLS_DIR/go1.26.0"
  tar -xzf "$MEERKAT_ARCHIVE" -C "$MEERKAT_TOOLS_DIR/go1.26.0"; rm -f "$MEERKAT_ARCHIVE"
  PATH="$MEERKAT_TOOLS_DIR/go1.26.0/go/bin:$PATH"
fi
PATH="$(dirname "$MEERKAT_NODE"):$PATH"; export PATH
exec "$MEERKAT_NODE" "$MEERKAT_SOURCE_DIR/scripts/install.mjs" "$@"
