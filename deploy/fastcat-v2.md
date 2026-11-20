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

## 共用 HTTPS 入口测试

`fastcat-v2 -web-root /var/www/catnode-https-test` 可以同时提供静态站点和
FastCat v2。站点根目录需要公开可读的 `index.html`。TCP 入口提供
HTTP/1.1 和 HTTP/2，UDP 入口提供 HTTP/3。CONNECT 继续使用原有授权与目标策略。

独立公网试验可使用 `catnode-https-test.service`，其测试二进制、证书、凭证
和网站目录独立配置。仅在 443/TCP 与 443/UDP 均可用的测试地址上启动；
如同机已有 HTTPS 服务，可使用专用测试 IP，修改单元的监听地址。
`deploy/website/index.html` 是可公开访问的测试页。公网浏览器可直接访问，
快猫使用同一域名和 443 作为 H2/H3 入口。比较另一入口时应记录客户端网络、
目标和成功率以及首字节分位数。

2026-11-20 的独立公网验证在同一台荣耀安卓、同一 Wi-Fi 上交替测试
原入口 18447 与 443。使用独立测试程序和不同入口各自的凭证，两个端口
都校验同一域名的证书。每个端口、每种协议发出 6 个短 HTTPS 请求，
从发起 CONNECT 到收到目标第一字节计时。两个测试目标各占一半。

| 入口 | H2 成功 / 首字节 P50 / P95 | H3 成功 / 首字节 P50 / P95 |
|---|---|---|
| 18447 | 6/6 · 438 / 727 ms | 6/6 · 620 / 1123 ms |
| 443 | 6/6 · 442.5 / 1200 ms | 6/6 · 726 / 1230 ms |

两个入口各有一次通过 H3 的 UDP STUN 回包，事务号匹配。公网访问 443 的
测试页面在 HTTP/1.1、HTTP/2 和 HTTP/3 均返回 200。本次运行未切换蜂窝网络，
未通过前置中转，并且样本不足以估计其他网络上的可用性或流量识别率。

## 面板管理的 443 入口

CatNode v0.4.0 的安装器支持 `--shared-https --port 443`。先在面板创建独立
FastCat v2 节点，将公网连接端口和服务端口设为 443，部署命令使用该节点 ID。
安装器启动 `catnode-fastcat-v2-443.service` 并配置独立的证书、计量状态
和网站目录。可更新 `/var/www/catnode-fastcat-v2-443/` 下的公开网页。
节点账号授权、流量倍率和订阅格式沿用面板管理模式。
