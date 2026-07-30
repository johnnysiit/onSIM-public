# 生产安装与运维

[English](INSTALL.md)

本指南将 onSIM 安装为两个 Rootless Podman 容器：

- `onsim`：Web/API、Provider、媒体、Telegram 和持久化
- `onsim-asterisk`：可选的 SIP 网桥

宿主机仅运行 Podman 用户服务、udev 规则和 USB 权限配置。

## 1. 环境要求

- Debian 12/13、Raspberry Pi OS，或其他支持 Rootless Podman 的系统
- `podman`、`crun`、`git`、`curl`、`openssl` 和 `systemd --user`
- 允许启用 lingering 用户服务的账号
- Android：已启用 USB 调试的兼容 Root 手机和 Companion
- SIM7600：可识别的串口/PCM 接口和充足的 USB 供电

不要以 Root 身份运行应用，也不要使用特权容器。

## 2. 克隆并检查

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
git status --short
```

运行脚本前先审阅内容。生产运行数据必须保存在仓库之外。

## 3. 选择 Provider

Android：

```sh
adb devices -l
export ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
```

在期待音频状态正常前，先完成 [Android 网关教程](ANDROID_GATEWAY.zh-CN.md)。

SIM7600：

```sh
export ONSIM_GATEWAY_MODE=sim7600
```

然后完成 [SIM7600 教程](SIM7600.zh-CN.md)中的硬件准入检查。

## 4. 安装

以目标桌面/登录用户运行，不要使用 `sudo`：

```sh
scripts/install-containers.sh
```

脚本仅在安装 udev 规则和配置用户组时使用 `sudo`，并创建：

```text
~/.config/onsim/onsim.env
~/.config/containers/systemd/onsim.container
~/.config/containers/systemd/onsim-asterisk.container
~/.local/share/onsim/
```

在私有环境文件中配置 Provider 和序列号：

```sh
chmod 600 ~/.config/onsim/onsim.env
editor ~/.config/onsim/onsim.env
systemctl --user daemon-reload
systemctl --user restart onsim-asterisk.service onsim.service
```

单台 Android 手机：

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
ONSIM_ANDROID_SUBSCRIPTION_ID=auto
```

多台手机请参考[配置说明](CONFIGURATION.zh-CN.md)中的 JSON 示例。

## 5. 首次登录与 HTTPS

端口 `8989` 提供健康检查、CA 下载和 HTTPS 重定向；端口 `9443` 提供应用服务。

1. 下载 `http://<主机IP>:8989/onsim-ca.crt`。
2. 仅在你管理的设备上安装该 CA。
3. 打开 `https://<主机IP>:9443`。
4. 创建至少 10 个字符的管理员密码。
5. 按需授予麦克风和通知权限。

从互联网访问时，优先使用 VPN，或通过受公众信任的证书终止 TLS。将
`ONSIM_PUBLIC_URL` 设置为 Telegram 实际使用的可信 HTTPS URL。私有 CA 必须
安装到手机信任库中；主机名不匹配无法安全绕过。

## 6. 安装 PWA

- Android Chrome：「设置 → 手机应用 → 安装 onSIM」
- iOS Safari：「分享 → 添加到主屏幕」

Web 应用需要安全上下文才能使用麦克风。Telegram 内置浏览器应将临时页面交给
系统浏览器打开。

## 7. 验证

```sh
systemctl --user status onsim.service onsim-asterisk.service
podman ps
curl -fsS http://127.0.0.1:8989/healthz
curl -kfsS https://127.0.0.1:9443/healthz
journalctl --user -u onsim.service -n 100 --no-pager
```

Android：

```sh
podman exec onsim adb -P 5038 devices -l
podman exec onsim adb -P 5038 forward --list
```

在使用自己拥有的号码完成短信双向测试和至少 30 秒双向通话测试前，不要宣布
生产环境已就绪。

## 8. 升级

停止拨打电话并确认当前没有活动通话，然后执行：

```sh
git pull --ff-only
scripts/install-containers.sh
```

镜像会被替换，`~/.local/share/onsim` 会保留。务必保存 Android APK 签名密钥，
确保 Companion 升级继续使用相同签名。

## 9. 备份与恢复

以下内容必须一同备份：

- SQLite 数据库和 WAL 文件
- 录音
- `master.key`
- Android 签名密钥和签名环境
- 客户端已信任的 TLS CA

没有匹配的 `master.key`，切勿恢复加密的数据库设置。停止服务，或使用
`scripts/backup.sh` 获取一致的 SQLite 备份。

## 10. 卸载

先停止用户服务：

```sh
systemctl --user disable --now onsim.service onsim-asterisk.service
```

删除容器或镜像不会删除 `~/.local/share/onsim`。只有在备份并验证成功后，才能
删除持久化数据。
