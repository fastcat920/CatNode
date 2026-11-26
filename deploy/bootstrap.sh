#!/usr/bin/env bash
set -euo pipefail

base=https://github.com/fastcat920/CatNode/releases/latest/download
[[ $(uname -s) == Linux && $EUID -eq 0 ]] || { echo '请在 Linux 上以 root 运行。' >&2; exit 1; }
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo '当前架构没有 fc-node 发布包。' >&2; exit 1 ;;
esac

bundle=fc-node-linux-$arch
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cd "$work"
for file in "$bundle.tar.gz" "$bundle.tar.gz.sha256"; do
  curl --proto '=https' --proto-redir '=https' -fsSL -o "$file" -- "$base/$file"
done
sha256sum --check --strict "$bundle.tar.gz.sha256"
tar -xzf "$bundle.tar.gz"
bash "$bundle/native-install.sh" "$@"
