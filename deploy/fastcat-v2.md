# CatNode 独立 FastCat v2

在项目根目录构建 Linux 版本：

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/catnode-linux-amd64 .
```

把二进制安装到 `/usr/local/bin/catnode`，把单元安装到
`/etc/systemd/system/catnode-fastcat-v2.service`。在
`/etc/catnode/fastcat-v2/` 存放与连接域名匹配的
`fullchain.pem`、`privkey.pem`，以及每行一个设备凭证的
`h2.tokens`、`h3.tokens`。私钥和令牌文件仅允许 root 读写。
两条入口使用同号端口，示例为 18447。按需开放 TCP 和 UDP。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now catnode-fastcat-v2
sudo systemctl status catnode-fastcat-v2
```

在安卓快猫的「FastCat v2 直连」配置相同的域名、H2 和 H3 端口及各自的凭证。
systemd 在启动时复制凭证文件。证书或令牌更新后，重启该单元以读取新内容。

## 本地混合运行

保留原有 `Nodes` 配置，在同一配置文件添加以下可选字段。该模式适合验证
Xray 和 FastCat v2 在同一进程中使用不同端口。

```json
{
  "FastCatV2": {
    "Listen": "0.0.0.0:18447",
    "Certificate": "/etc/catnode/fastcat-v2/fullchain.pem",
    "PrivateKey": "/etc/catnode/fastcat-v2/privkey.pem",
    "H2Tokens": "/etc/catnode/fastcat-v2/h2.tokens",
    "H3Tokens": "/etc/catnode/fastcat-v2/h3.tokens",
    "TLSKeyExchange": "default"
  }
}
```

使用原有 `catnode server -c /etc/v2node/config.json` 启动。修改 FastCat v2
的入口地址或证书需重启进程。直接读取本地凭证文件时，可以发送 SIGHUP
重新读取令牌。当前 FastCat v2 使用文件授权，Xray 节点继续通过原面板接口管理。

## 面板自动同步凭证

先更新 Catboard，保留面板与设备网关共用的 `FASTCAT_DEVICE_GRANT_KEY`。
将 Linux x86_64 安装包上传到节点服务器，解压后运行：

```sh
tar -xzf catnode-linux-amd64.tar.gz
cd catnode-linux-amd64
sudo bash install.sh
```

按提示输入面板 HTTPS 地址、节点 ID、节点证书域名、端口和节点通讯 Token。
也可以提供 `--api-host`、`--node-id`、`--domain`、`--port` 和
`--api-key-file`。默认使用节点域名的 Let's Encrypt 证书，可通过
`--cert` 和 `--key` 指定已有证书。证书续期后可沿用原参数重新安装，
更新 systemd 读取的证书副本。

安装程序会预检面板授权接口，写入配置并启动 `catnode-fastcat-v2`。
服务启动时拉取授权，之后每五秒同步一次。授权快照过期时拒绝新的会话，
恢复连接后自动同步。已通过设备证明的快猫刷新订阅时会自动获得凭证。
入口需允许同端口的 TCP 和 UDP。
