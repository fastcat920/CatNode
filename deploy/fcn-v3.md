# FCN v3 接入

在 Catboard 的 V2Node 节点选择 `fcn-v3`，设置节点域名、监听端口、权限组和倍率。选择 HTTP 自动申请、DNS 自动申请或已有文件证书。HTTP 验证要求公网 TCP 80 可达且节点可以监听，DNS 验证要求节点配置 DNS 服务商及凭据。已有证书的 SAN 须覆盖订阅中的 SNI，证书链须受客户端系统信任。新握手读取轮换后的证书与私钥，已建立的连接继续使用原握手状态。

使用 CatNode 的原生 V2Node 安装入口。公网入口使用 **UDP 监听端口**，客户端的 TCP 业务也封装在 QUIC 中。订阅中的节点类型为 `fcn`，版本为 `3`，使用支持 FCN v3 的新版 FastCat App。

## 账号、路由与计量

节点从现有面板用户接口读取权限组授权的账号 UUID。App 与节点从同一 UUID 派生 FCN v3 的账号标识和认证密钥。节点在完整的 TLS 1.3 握手后按 CONNECT 请求验证证明。用户列表更新会撤销被删除或轮换的凭证，并关闭该凭证正在使用的 TCP 流和 UDP 关联。

目标支持公网 IP 和域名。域名通过节点配置的 Xray DNS 解析，实际目标固定为校验通过的公网地址。Xray 路由仍能读取原始域名和选定的 IP。包含私有地址或保留地址的解析结果被拒绝。面板配置的转发和阻断规则继续生效。

节点通过 Xray dispatcher 记录每个账号的上下行业务字节，继续按面板 UID 上报并应用节点倍率。UDP 关联绑定已授权的单一目标，应用载荷上限为 1,150 字节。TCP 半关闭向 Xray 分发管道传递 EOF 并保留下行。当前 Xray Freedom 出站在该情形下不会向目标 TCP 套接字发送 FIN，等待目标侧 FIN 的应用可能无法完成请求。

## 源码同步和验证

App 仓库的 `core/fcnv3` 是共享协议实现。CatNode 的 `protocol/fcnv3` 随项目构建，改变线协议或认证时应同步两端。

```sh
python3 tools/sync-fcn.py /path/to/FastCatAPP --v3 --check
go vet ./...
go test ./core -run '^TestFCN'
```

共享实现更新后移除 `--check` 参数可从 App 同步到 CatNode。验证覆盖证书轮换、Xray TCP 与 UDP 路由、账号计量、已建立连接撤销和 FCN v1/v2 回归。
