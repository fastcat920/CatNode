#!/usr/bin/env bash
set -euo pipefail

if [[ ${1:-} == '--help' || ${1:-} == '-h' ]]; then
  echo '用法：bash catnode-install.sh --api-host HTTPS_URL --node-id ID --domain DOMAIN --port PORT [--api-key KEY | --api-key-file FILE]'
  exit 0
fi
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 || $EUID -ne 0 ]]; then
  echo '请在 Linux x86_64 节点服务器以 root 运行。' >&2
  exit 1
fi
for program in curl sha256sum tar; do
  command -v "$program" >/dev/null || { echo "缺少命令：$program" >&2; exit 1; }
done

base=https://github.com/fastcat920/CatNode/releases/download/v0.3.0
archive=catnode-linux-amd64.tar.gz
work=$(mktemp -d)
cleanup() {
  rm -r -- "$work"
}
trap cleanup EXIT
cd "$work"
curl --proto '=https' -fsSLO "$base/$archive"
curl --proto '=https' -fsSLO "$base/$archive.sha256"
sha256sum -c "$archive.sha256"
tar -xzf "$archive"
bash catnode-linux-amd64/install.sh "$@"
