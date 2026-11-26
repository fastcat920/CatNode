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
[[ -x $bundle/fc-node ]] || { echo '请在 fc-node 发布包中运行。' >&2; exit 1; }
config_dir=/etc/catnode/native-$node_id
service=catnode-native-$node_id
install -d -m 700 "$config_dir"
# Existing native certificates default to this shared directory.
install -d -m 700 /etc/v2node
umask 077
exec 9>"$config_dir/install.lock"
flock -x 9
unit=/etc/systemd/system/$service.service
stage=$(mktemp -d "$config_dir/.install.XXXXXX")
unit_stage=$(mktemp "$unit.XXXXXX")
trap 'rm -rf -- "$stage"; rm -f -- "$unit_stage"' EXIT
install -m 755 "$bundle/fc-node" "$stage/fc-node"
printf '{"Log":{"Level":"info","Access":"none"},"Nodes":[{"ApiHost":"%s","NodeID":%s,"ApiKey":"%s"}]}\n' "${api_host%/}" "$node_id" "$api_key" > "$stage/config.json"
cat > "$unit_stage" <<UNIT
[Unit]
Description=fc-node native node $node_id
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=$config_dir/fc-node server -c $config_dir/config.json
WorkingDirectory=$config_dir
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true
[Install]
WantedBy=multi-user.target
UNIT
chmod 644 "$unit_stage"
for file in fc-node config.json; do
  if [[ -f $config_dir/$file ]]; then
    cp -p -- "$config_dir/$file" "$stage/$file.backup"
  fi
done
if [[ -f $unit ]]; then cp -p -- "$unit" "$stage/unit.backup"; fi

mv -f -- "$stage/fc-node" "$config_dir/fc-node"
mv -f -- "$stage/config.json" "$config_dir/config.json"
mv -f -- "$unit_stage" "$unit"
if ! { systemctl daemon-reload && systemctl restart "$service" &&
       sleep 4 && systemctl is-active --quiet "$service" && systemctl enable "$service"; }; then
  systemctl stop "$service" || true
  for file in fc-node config.json; do
    if [[ -f $stage/$file.backup ]]; then
      mv -f -- "$stage/$file.backup" "$config_dir/$file"
    else
      rm -f -- "$config_dir/$file"
    fi
  done
  if [[ -f $stage/unit.backup ]]; then
    install -m 644 "$stage/unit.backup" "$unit"
    systemctl daemon-reload && systemctl restart "$service" || true
  else
    rm -f -- "$unit"
    systemctl daemon-reload || true
  fi
  echo "fc-node 启动失败，请查看 journalctl -u $service。" >&2
  exit 1
fi
echo "fc-node 节点 $node_id 已启动；协议、证书、账号和计量参数均由面板同步。请按面板协议放行对应的 TCP 或 UDP 端口。"
