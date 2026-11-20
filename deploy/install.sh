#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
用法：sudo bash install.sh [--api-host HTTPS_URL] [--node-id ID]
                         [--api-key KEY | --api-key-file FILE]
                         [--domain DOMAIN] [--port PORT]
                         [--cert FILE --key FILE] [--shared-https]

省略通讯 Token 时会隐藏输入。未指定证书路径时，使用
/etc/letsencrypt/live/<域名>/fullchain.pem 和 privkey.pem。
USAGE
}

api_host='' node_id='' api_key='' api_key_file='' domain='' port='' cert='' key=''
shared_https=false
while (($#)); do
  case "$1" in
    --shared-https) shared_https=true; shift ;;
    -h|--help) usage; exit 0 ;;
    --api-host|--node-id|--api-key|--api-key-file|--domain|--port|--cert|--key)
      (($# >= 2)) || { usage >&2; exit 2; }
      case "$1" in
        --api-host) api_host=$2 ;;
        --node-id) node_id=$2 ;;
        --api-key) api_key=$2 ;;
        --api-key-file) api_key_file=$2 ;;
        --domain) domain=$2 ;;
        --port) port=$2 ;;
        --cert) cert=$2 ;;
        --key) key=$2 ;;
      esac
      shift 2
      ;;
    *) usage >&2; exit 2 ;;
  esac
done

die() { printf '%s\n' "$*" >&2; exit 1; }
prompt() {
  local field=$1 label=$2 default=${3:-}
  [[ -z ${!field} ]] || return 0
  if [[ ! -t 0 ]]; then
    [[ -n $default ]] || die "缺少参数：$label"
    printf -v "$field" '%s' "$default"
    return 0
  fi
  if [[ -n $default ]]; then label="$label [$default]"; fi
  read -r -p "$label: " "$field"
  [[ -n ${!field} ]] || printf -v "$field" '%s' "$default"
}

[[ $(uname -s) == Linux && $EUID -eq 0 ]] || die '请在 Linux 节点服务器以 root 运行。'
for command in systemctl curl openssl; do
  command -v "$command" >/dev/null || die "缺少命令：$command"
done
bundle=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ -x $bundle/catnode ]] || die '请在解压后的 CatNode 发布包中运行安装脚本。'
case "$(uname -m)" in
  x86_64) ;;
  *) die '此安装包适用于 Linux x86_64。' ;;
esac

prompt api_host '面板 HTTPS 地址'
prompt node_id '节点 ID'
prompt domain '节点证书域名'
if $shared_https; then
  prompt port '节点连接端口' 443
  [[ $port == 443 ]] || die '共用 HTTPS 模式需要在 443 上监听。'
else
  prompt port '节点连接端口' 18447
fi
api_host=${api_host%/}
[[ $api_host =~ ^https://[[:alnum:].:-]+(/[[:alnum:]._/-]*)?$ ]] || die '请输入面板的 HTTPS 地址。'
[[ $node_id =~ ^[1-9][0-9]{0,9}$ ]] || die '节点 ID 必须为正整数。'
[[ $port =~ ^[1-9][0-9]{0,4}$ ]] && ((port <= 65535)) || die '端口必须在 1–65535。'
[[ $domain =~ ^[[:alnum:]][[:alnum:].-]*$ ]] || die '请输入节点证书域名。'
if [[ -n $api_key_file ]]; then
  [[ -z $api_key ]] || die '请只选择一种 Token 输入方式。'
  api_key=$(<"$api_key_file")
elif [[ -z $api_key ]]; then
  [[ -t 0 ]] || die '缺少节点通讯 Token。'
  read -rs -p '面板节点通讯 Token: ' api_key
  printf '\n'
fi
[[ -n $api_key && $api_key != *$'\n'* && $api_key != *$'\r'* && $api_key != *'"'* && $api_key != *'\'* ]] ||
  die '节点通讯 Token 格式无效。'

cert=${cert:-/etc/letsencrypt/live/$domain/fullchain.pem}
key=${key:-/etc/letsencrypt/live/$domain/privkey.pem}
[[ -r $cert && -r $key ]] || die '证书或私钥不可读。请先签发证书，或指定 --cert 和 --key。'
openssl x509 -in "$cert" -noout -checkend 0 >/dev/null || die '证书无效或已过期。'
cert_info=$(openssl x509 -in "$cert" -noout -checkhost "$domain")
[[ $cert_info == *'does match certificate'* ]] || die '证书与节点域名不匹配。'
cert_public=$(openssl x509 -in "$cert" -pubkey -noout)
key_public=$(openssl pkey -in "$key" -pubout)
[[ $cert_public == "$key_public" ]] || die '证书与私钥不匹配。'

# Check panel authorization before changing the current node service.
if ! status=$(printf 'header = "Authorization: Bearer %s"\n' "$api_key" |
  curl --config - --proto '=https' --silent --show-error --fail --max-time 12     --header 'Accept: application/json' --output /dev/null --write-out '%{http_code}'     "$api_host/api/v1/server/fastCat/grants?node_id=$node_id") || [[ $status != 200 ]]; then
  die '面板同步接口不可用。请检查面板已更新、节点 ID 和通讯 Token 正确。'
fi

umask 077
config=/etc/catnode/fastcat-v2
unit=catnode-fastcat-v2.service
web_root=''
if $shared_https; then
  config=/etc/catnode/fastcat-v2-443
  unit=catnode-fastcat-v2-443.service
  web_root=/var/www/catnode-fastcat-v2-443
  [[ -r $bundle/site/index.html || -r $web_root/index.html ]] ||
    die '缺少共用 HTTPS 的网站首页。'
fi
unit_path=/etc/systemd/system/$unit
dropin=$unit_path.d/managed.conf
binary=/usr/local/bin/catnode
if $shared_https; then binary=/usr/local/bin/catnode-443; fi
install -d -m 0700 "$config"
install -d -m 0755 "$(dirname "$dropin")" "$(dirname "$binary")"
backup=$(mktemp -d)
paths=("$binary" "$unit_path" "$dropin" "$config/fullchain.pem" "$config/privkey.pem" "$config/panel.env" "$config/panel.token")
was_active=false
systemctl is-active --quiet "$unit" && was_active=true
for i in "${!paths[@]}"; do
  if [[ -e ${paths[i]} || -L ${paths[i]} ]]; then cp -a -- "${paths[i]}" "$backup/$i"; fi
done

committed=false
cleanup() {
  if ! $committed; then
    systemctl stop "$unit" || true
    for i in "${!paths[@]}"; do
      if [[ -e $backup/$i || -L $backup/$i ]]; then
        cp -a --remove-destination -- "$backup/$i" "${paths[i]}" || true
      else
        rm -- "${paths[i]}" 2>/dev/null || true
      fi
    done
    systemctl daemon-reload || true
    if $was_active; then systemctl start "$unit" || true; fi
  fi
  find "$backup" -maxdepth 1 -type f -delete
  find "$backup" -maxdepth 1 -type l -delete
  rmdir -- "$backup"
}
trap cleanup EXIT

install -m 0755 "$bundle/catnode" "$binary.new"
mv -- "$binary.new" "$binary"
install -m 0644 "$bundle/catnode-fastcat-v2.service" "$unit_path"
install -m 0644 "$bundle/catnode-fastcat-v2-managed.conf" "$dropin"
if $shared_https; then
  sed -i 's|/etc/catnode/fastcat-v2/|/etc/catnode/fastcat-v2-443/|g; s|/usr/local/bin/catnode |/usr/local/bin/catnode-443 |g' "$dropin"
  sed -i '/^DynamicUser=yes/a AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE' "$unit_path"
  sed -i 's/^StateDirectory=catnode-fastcat-v2$/StateDirectory=catnode-fastcat-v2-443/' "$unit_path"
  install -d -m 0755 "$web_root"
  if [[ ! -e $web_root/index.html ]]; then
    install -m 0644 "$bundle/site/index.html" "$web_root/index.html"
  fi
fi
if [[ ! "$cert" -ef "$config/fullchain.pem" ]]; then install -m 0600 "$cert" "$config/fullchain.pem"; fi
if [[ ! "$key" -ef "$config/privkey.pem" ]]; then install -m 0600 "$key" "$config/privkey.pem"; fi
chmod 0600 "$config/fullchain.pem" "$config/privkey.pem"
printf '%s\n' "$api_key" > "$config/panel.token"
chmod 0600 "$config/panel.token"
printf 'FASTCAT_PANEL_URL=%s\nFASTCAT_NODE_ID=%s\nFASTCAT_PORT=%s\nFASTCAT_WEB_ROOT=%s\n' "$api_host" "$node_id" "$port" "$web_root" > "$config/panel.env"
systemctl daemon-reload
systemctl restart "$unit"
sleep 2
systemctl is-active --quiet "$unit" || die 'CatNode 启动失败，正在恢复原配置。'
systemctl enable "$unit"
committed=true
printf 'CatNode 已启动，节点 %s，端口 %s（TCP 和 UDP）。现在可以在快猫刷新订阅。\n' "$node_id" "$port"
