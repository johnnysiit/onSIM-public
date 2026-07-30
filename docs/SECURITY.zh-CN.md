# 安全与隐私指南

[English](SECURITY.md)

## onSIM 存储的数据

根据启用的功能，数据目录可能包含：

- 管理员密码哈希和会话
- 通话记录和短信内容
- 电话号码、SIM/设备元数据和过滤规则
- 由 `master.key` 加密的 Telegram、Provider 和 SIP 凭据
- 通话录音和语音信箱
- TLS CA/私钥、ADB 密钥、Companion Token 和 Android 签名材料

整个数据目录都应视为敏感数据。

## 仓库安全

切勿提交：

- `.env` 或生产环境文件
- SQLite 数据库、WAL/SHM 文件、日志、录音或备份
- 真实电话号码、IMEI/IMSI/ICCID、设备序列号或公网部署 IP
- Telegram Token、Chat ID、Provider Key、SIP 密码或主密钥
- ADB 私钥、TLS 私钥、APK Keystore、固件、OTA 或 Boot 镜像

发布前检查：

```sh
git status --short --ignored
git grep -n -I -E '(BEGIN .*PRIVATE KEY|bot[0-9]+:|/home/[^/]+)'
git log --all --format='%h %an <%ae>'
```

使用 Gitleaks 等专用工具扫描完整 Git 历史。新提交中删除文件不能将其从旧提交
移除。任何曾进入 Git 的凭据都必须轮换。

## 网络暴露

- 优先通过局域网或 VPN 访问。
- WebRTC 麦克风访问必须使用浏览器信任的 HTTPS。
- 不要公开 ADB、Asterisk SIP/RTP、控制 Socket 或数据库端口。
- 将 Telegram 访问限制为配置的 Chat ID。
- 使用长且唯一的管理员密码。
- 临时通话链接应设置较短有效期，并仅通过可信聊天发送。

私有 CA 只适合明确安装了该 CA 的受管设备。公共用户需要公众信任的证书，且
证书必须包含实际使用的主机名/IP。

## 手机与 Modem

Android Companion 拥有电话、短信和音频特权。手机应专用于网关；严格控制 USB
调试的物理访问，不要授权未知电脑，并保护 Recovery 和 Bootloader 入口。

不要修改设备身份值来绕过运营商控制。SIM7600 AT 端口具有敏感能力，不能提供
给不可信用户或容器。

## 录音与保留

不同司法辖区对录音和消息保留的规定不同。请履行必要的告知/同意义务，限制
访问权限，制定保留期限，并安全删除导出文件和备份。部署 onSIM 本身并不能
保证符合法律要求。

## 事件响应

如果部署或仓库发生泄漏：

1. 禁用外部访问。
2. 吊销 Telegram/Provider 凭据，并重置 SIP/管理员密码。
3. 只有在规划好设置迁移后才能更换 `master.key`。
4. 按需轮换 TLS CA、ADB 授权、Companion Token 和 APK 签名密钥。
5. 如果敏感 Blob 已提交，重写 Git 历史。
6. 尽可能清除缓存/分叉，并记录事件。
