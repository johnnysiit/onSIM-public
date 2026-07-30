# onSIM

[English](README.md)

onSIM 是一套自托管电话与短信网关。它把 Android 手机或受支持的 SIM7600
USB 模块接入网页、Telegram 和可选的 SIP 客户端，通话记录、短信和录音均保留
在自己的设备上。

> 本项目会操作真实电话和短信。对外开放前请阅读
> [安全与隐私指南](docs/SECURITY.zh-CN.md)。它不能替代紧急呼叫或生命安全设备。

## 功能

- 适配手机并可安装到主屏幕的 PWA
- 呼入、呼出、接听、拒接、挂起、DTMF、静音、手动录音和通话记录
- WebRTC 浏览器音频与蜂窝通话音频桥接
- Unicode/长短信、会话、发送状态、批量已读和删除
- 可开关的语音信箱、自录提示语、留言播放、下载和删除
- Telegram 拨号/短信对话、确认按钮、临时通话页面和来电操作
- 可选 Asterisk/PJSIP 软电话接入
- 双卡、多手机和明确的号码选择
- 本地白名单/黑名单及可选 HTTPS 号码识别服务
- rootless Podman、SQLite、凭据加密、健康检查和自动恢复
- 无硬件开发使用的 `mock` Provider

## 支持的网关

| Provider | 定位 | 注意事项 |
| --- | --- | --- |
| Android | 主要方案 | 需要 USB 调试、Magisk/root、onSIM Companion 和兼容的通话音频路由 |
| SIM7600 | 取决于硬件 | 受模块固件、运营商注册、VoLTE/语音回落和 USB PCM 支持影响 |
| Mock | 开发测试 | 不会真正拨号或发送短信 |

Android 方案目前以匹配原厂 Android 10 固件的一加 5 为开发目标。其他手机可能
需要单独适配 Audio Policy。严禁跨版本刷入 `boot.img`。

## 五分钟体验

先用 mock 模式体验界面，不连接电话卡：

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
podman build -t localhost/onsim:dev .
mkdir -p .local-data
podman run --rm --name onsim-dev \
  --network host \
  -e ONSIM_GATEWAY_MODE=mock \
  -e ONSIM_LISTEN=127.0.0.1:8989 \
  -e ONSIM_DATA_DIR=/var/lib/onsim \
  -v "$PWD/.local-data:/var/lib/onsim:Z" \
  localhost/onsim:dev
```

访问 `http://127.0.0.1:8989`，创建管理员密码。Mock 模式不会联系运营商，请只
使用虚构测试号码。

## 正式安装

推荐使用 rootless Podman + Quadlet：

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
scripts/install-containers.sh
```

安装程序构建一个不可变镜像，并分别启动 onSIM 与 Asterisk 用户容器。运行数据
不放在源码目录：

- `~/.config/onsim/onsim.env`：部署配置
- `~/.local/share/onsim`：数据库、录音、证书、ADB 密钥和运行密钥

随后根据硬件继续：

- [完整安装与运维教程](docs/INSTALL.zh-CN.md)
- [Android 网关教程](docs/ANDROID_GATEWAY.zh-CN.md)
- [SIM7600 教程](docs/SIM7600.zh-CN.md)

安装后先从 `http://<主机IP>:8989/onsim-ca.crt` 导入本地 CA，再访问
`https://<主机IP>:9443`。对公网开放时，应使用公网可信证书或通过可信 VPN
访问，不要让使用者忽略证书错误。

## 安装为手机应用

- Android Chrome：打开「设置 → 手机应用 → 安装 onSIM」
- iPhone/iPad Safari：点击「分享 → 添加到主屏幕」
- Telegram 内置浏览器：先选择在系统浏览器中打开

安装后可像普通 App 一样独立启动，并提供拨号、短信和设置快捷入口。麦克风权限
仍由浏览器和手机系统管理，网页不能绕过权限规则。

## 文档

- [安装与运维](docs/INSTALL.zh-CN.md)
- [Android 网关](docs/ANDROID_GATEWAY.zh-CN.md)
- [SIM7600 网关](docs/SIM7600.zh-CN.md)
- [配置参考](docs/CONFIGURATION.zh-CN.md)
- [安全与隐私](docs/SECURITY.zh-CN.md)
- [开发与测试](docs/DEVELOPMENT.zh-CN.md)
- [参与贡献](CONTRIBUTING.md)
- [报告安全问题](SECURITY.md)

## 架构

```text
手机/桌面 PWA ─ HTTPS/WebRTC ─┐
Telegram Bot ── Bot API ──────┼── onSIM（Go）
SIP 软电话 ──── Asterisk ─────┘       │
                                     ├── SQLite 与录音
                                     └── Gateway Provider
                                          ├── Android Companion（私有 ADB 转发）
                                          ├── SIM7600（AT + USB PCM）
                                          └── Mock
```

Go 服务统一管理通话状态、音频所有权、短信状态、防骚扰、鉴权、Telegram 和
持久化。Vue 前端通过 REST 下发命令，并通过 WebSocket 获取实时更新。Telegram
Token、Provider API Key 和 SIP 密码由本地 master key 加密，普通状态接口不会
返回这些值。

## 开发

```sh
sudo apt-get install -y golang-go nodejs npm gcc pkg-config \
  libsqlite3-dev libopus-dev libopusfile-dev opus-tools
make build
make test
```

生产 Containerfile 在构建时也会运行完整 Go 测试。更多说明见
[开发文档](docs/DEVELOPMENT.zh-CN.md)。

## 限制与责任

- 蜂窝能力受手机/模块固件、运营商网络和 SIM 套餐影响。
- Android 通话音频需要系统级权限，普通未 root 应用无法完成全部功能。
- Telegram 临时通话页面需要浏览器信任的 HTTPS 才能使用麦克风。
- 浏览器自动播放和麦克风授权不能被网页代码绕过。
- 使用者负责遵守所在地关于录音告知、同意、保存期限和访问控制的规定。

固件、boot 镜像、SIM 信息、APK 签名密钥、ADB 私钥、数据库、录音、
Telegram Token 和 TLS 私钥都不应提交到仓库。

## 许可证

onSIM 使用 [MIT License](LICENSE) 发布。
