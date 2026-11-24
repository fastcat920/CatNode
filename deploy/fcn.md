# FCN v1 / FCN v2

FCN v1 是已测试的 Next 单连接配置；FCN v2 是恢复状态选路配置。两者是 FCN 的两个固定客户端调度配置，共用 FCN/1 线格式、QUIC、TLS 1.3、BBR 和账号认证。它们不是原有 FastCat HTTPS v1/v2，也不是 TUIC 或 Hysteria 的别名。

| 面板类型 | 订阅 | 客户端策略 |
|---|---|---|
| `fcn-v1` / FCN v1 | `type: fcn`, `version: 1` | 一个连接池，保留受限连接轮换 |
| `fcn-v2` / FCN v2 | `type: fcn`, `version: 2` | 两个连接池，按恢复状态为新业务选路 |

不重放已发送业务。没有 H2 备用入口。QUIC 不可用时连接失败，不切换到其他协议。

## 面板、订阅与授权

直接在 V2node 节点编辑页选 FCN v1 或 FCN v2；无需借用 VLESS 类型。填写节点域名、连接端口、服务端口、权限组、倍率，以及与客户端 SNI 匹配的有效 TLS 证书配置。公网放行 **UDP 服务端口**。TCP 业务也通过 QUIC 流承载。

账号权限、到期、流量、限速和在线 IP 管理复用现有 V2node 接口。用账号 UUID 派生认证密钥，OPEN 的 HMAC 绑定 TLS exporter 和随机 nonce。不额外签发设备证明或维护授权目录。UUID 是秘密凭证，不应公开。

新版 App 根据原生核心导出的 `fastCatSupportsFCN` 自动添加 `fc_fcn=1`。FCN 下发不按客户端版本过滤，只按正常账号权限组规则。客户端实际连接仍需支持该协议及所选验证方式。加密订阅信封仍为 `fastcat-v1`，与传输协议版本无关。

```yaml
- name: FCN v2
  type: fcn
  version: 2
  server: node.example
  port: 443
  sni: node.example
  uuid: <account-uuid>
  udp: true
```

## 部署

节点编辑页生成 CatNode `--native` 命令。发布包内也可直接运行：

```sh
bash install.sh --native --api-host https://panel.example --node-id 123
```

未传 token 时隐藏输入，或使用 `--api-key-file`。服务为 `catnode-native-123.service`，配置在 `/etc/catnode/native-123/config.json`，权限 600；协议、用户和证书设置从面板同步，支持原有文件/远程/ACME 证书配置。文件证书在新握手时重新读取，续期后无需中断现有连接。

FCN 对应 **CatNode v0.5.0**。面板生成的下载命令固定使用此版本；不能用旧 v0.4.0 包部署 FCN。发布包含 Linux amd64/arm64 的 tar 包、SHA256 校验文件和 bootstrap；本地均完成交叉编译。

## 计量

FCN 已认证连接进入 CatNode 原有 Xray dispatcher，复用路由、限速、在线 IP 和按 UID 的上下行计数。计费统计业务载荷，不把 QUIC、TLS、重传等网络开销计入账号用量；面板继续应用节点倍率。

计数抽取使用原子交换；首次并发连接使用同一个原子注册的节点计数器。HTTP 上报失败保留本批数据，在后续周期重试。与现有接口一致，这不是持久化、恰好一次账本：进程退出仍可能丢失待上报内存批次，响应丢失后的重试需要服务端幂等扩展才能消除重复计费歧义。

## 当前限制

UDP 使用独立认证关联及 QUIC DATAGRAM，有效 UDP 载荷上限 1100 字节，超限返回错误；没有新增分片能力。FCN 不继承旧 FastCat v2 的 ECH、共享 HTTPS 网站或 H2/H3 自动选路。默认验证域名和证书，不提供跳过校验选项。当前验证不代表隐蔽性评估或跨网络性能保证。

## 自检

```sh
go test ./...
(cd protocol/fcn && go test -race ./...)
go test -race ./core ./node -run 'TestFCN|TestFailedTraffic'
python3 tools/sync-fcn.py /path/to/FastCatAPP --check
```

`protocol/fcn` 为规范实现；App 内置副本需用同步脚本保持完全一致。BBR 实现保留 Mihomo 原有代码及 GPL 授权声明。

## 自签证书 + 固定公钥（2026-11-24）

新增显式 `tls-trust: spki` 与 `server-public-key-pins`。使用 SHA256(DER SubjectPublicKeyInfo)，仍验证叶证书名称、有效期和服务器认证用途。错误 pin、缺失 pin 或格式错误会失败；不会回退到 CA 或跳过校验。

面板管理模式生成含 SAN 的 ECDSA P-256 证书，使用现有 `remote` 证书配置发给节点；CatNode v0.5.0 的服务端已支持，不要求仅为此功能重新部署。到期前用同一密钥续签不改变 SPKI。换密钥需明确操作并刷新订阅，当前面板不提供双阶段零中断换钥。

节点可使用 `www.bing.com` 作为自签证书名称，但身份来自固定公钥，不代表 Bing 官方身份；没有因此验证隐蔽性提升。已有 CA 节点不会自动转换。节点下发仅按账号权限组，不按 FCN 能力标记筛选。
