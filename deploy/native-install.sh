#!/usr/bin/env bash
set -euo pipefail
# FCN uses the standard panel config/user/traffic APIs, like TUIC and Hysteria 2.
api_host='' node_id='' api_key='' api_key_file=''
while (($#)); do
  (($# >= 2)) || { echo '用法: --api-host HTTPS_URL --node-id ID [--api-key TOKEN | --api-key-file FILE]' >&2; exit 2; }
  case "$1" in
    --api-host) api_host=$2 ;;
    --node-id) node_id=$2 ;;
    --api-key) api_key=$2 ;;
    --api-key-file) api_key_file=$2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
  shift 2
done
[[ $(uname -s) == Linux && $EUID -eq 0 ]] || { echo '请在 Linux 上以 root 运行。' >&2; exit 1; }
[[ $api_host =~ ^https://[[:alnum:].:-]+(/[[:alnum:]._/-]*)?$ && $node_id =~ ^[1-9][0-9]{0,9}$ ]] || { echo '面板地址或节点 ID 无效。' >&2; exit 2; }
if [[ -n $api_key_file ]]; then
 [[ -z $api_key ]] || exit 2
 api_key=$(<"$api_key_file")
elif [[ -z $api_key ]]; then
 read -rs -p '面板节点通讯 Token: ' api_key
 printf '\n'
fi
[[ -n $api_key && $api_key != *[$'\001'-$'\037']* && $api_key != *'"'* && $api_key != *'\'* ]] || { echo 'Token 格式无效。' >&2; exit 2; }
bundle=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ -x $bundle/catnode ]] || { echo '请在 CatNode 发布包中运行。' >&2; exit 1; }
config_dir=/etc/catnode/native-$node_id
service=catnode-native-$node_id
install -d -m 700 "$config_dir"
# Existing native certificates default to this shared directory.
install -d -m 700 /etc/v2node
install -m 755 "$bundle/catnode" "$config_dir/catnode"
umask 077
if [[ -f $config_dir/config.json ]]; then cp -p "$config_dir/config.json" "$config_dir/config.json.bak"; fi
printf '{"Log":{"Level":"info","Access":"none"},"Nodes":[{"ApiHost":"%s","NodeID":%s,"ApiKey":"%s"}]}\n' "${api_host%/}" "$node_id" "$api_key" > "$config_dir/config.json"
cat > "/etc/systemd/system/$service.service" <<UNIT
[Unit]
Description=CatNode native node $node_id
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=$config_dir/catnode server -c $config_dir/config.json
WorkingDirectory=$config_dir
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable "$service"
systemctl restart "$service"
systemctl is-active --quiet "$service"
echo "CatNode 节点 $node_id 已启动；协议、证书、账号和计量参数均由面板同步。请放行面板配置的 UDP 服务端口。"
