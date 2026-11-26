# fc-node

fc-node 是 Catboard 节点后端，支持 Shadowsocks、VMess、VLESS、Trojan、TUIC、Hysteria 2、AnyTLS 和 FCN v1、v2、v3。

## 部署节点

在 Catboard 中保存节点，复制一键安装指令，在 Linux amd64 或 arm64 服务器以 root 执行。安装器选择对应架构的发行包并校验 SHA-256。从面板同步协议、账号、证书和流量配置。同一个节点 ID 再次执行会升级原有 native 服务。

现有 FastCat v2 节点需要在面板里改用受支持的协议。迁移前先核对旧服务使用的端口与证书路径，并在切换端口时停用旧服务。FCN v1 和 v2 可使用面板配置的证书或固定服务端公钥，FCN v3 使用受信任的证书。按面板配置放行对应的 TCP 或 UDP 端口。

## 上游与许可证

上游源码基线为 [v2node 5c2360f](https://github.com/wyx2685/v2node/commit/5c2360f95db39f61271e701eabdee200ba8075ea)。
原 v2node 源码及其许可证见 [LICENSE](LICENSE)。引入的 Mihomo 代码保留
[原许可证](internal/fastcatv2/LICENSE-mihomo)。

<details>
<summary>v2node 上游说明</summary>

# v2node
A v2board backend base on moddified xray-core.
一个基于修改版xray内核的V2board节点服务端。

**注意： 本项目需要搭配[修改版V2board](https://github.com/wyx2685/v2board)**

## 软件安装

### 一键安装

```
wget -N https://raw.githubusercontent.com/wyx2685/v2node/master/script/install.sh && bash install.sh
```

## 构建
``` bash
GOEXPERIMENT=jsonv2 go build -v -o build_assets/v2node -trimpath -ldflags "-X 'github.com/wyx2685/v2node/cmd.version=$version' -s -w -buildid="
```

## Stars 增长记录

[![Stargazers over time](https://starchart.cc/wyx2685/v2node.svg?variant=adaptive)](https://starchart.cc/wyx2685/v2node)

</details>

## FCN v1 / FCN v2

Next 单连接正式名称为 **FCN v1**，恢复状态选路正式名称为 **FCN v2**。两者已接入标准节点生命周期、账号 UUID、限速与计量；部署与兼容性见 [FCN 接入说明](deploy/fcn.md)。

## FCN v3

FCN v3 通过 HTTP/3 CONNECT 转发 TCP，并通过 CONNECT-UDP 转发 UDP。标准 V2Node 节点使用面板的账号、路由、限速和流量统计。证书要求、部署方式与已知限制见 [FCN v3 接入说明](deploy/fcn-v3.md)。
