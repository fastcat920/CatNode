# CatNode

CatNode 是基于 [v2node](https://github.com/wyx2685/v2node) 的 Catboard 节点后端，
支持原有 Xray 节点和 FastCat v2。FastCat v2 提供 HTTP/2 和 HTTP/3 入口，
可从 Catboard 自动同步设备授权。

## 安装 FastCat v2 节点

在 Linux x86_64 节点服务器执行：

```bash
curl -fLO https://github.com/fastcat920/CatNode/releases/download/v0.4.0/catnode-linux-amd64.tar.gz
tar -xzf catnode-linux-amd64.tar.gz
cd catnode-linux-amd64
sudo bash install.sh
```

安装器会提示面板 HTTPS 地址、节点 ID、证书域名、端口和节点通讯 Token。
部署细节见 [FastCat v2 部署说明](deploy/fastcat-v2.md)。

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
