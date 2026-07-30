# 配置参考

[English](CONFIGURATION.md)

生产配置保存在权限为 `0600` 的 `~/.config/onsim/onsim.env` 中。不要在仓库内
创建真实的 `.env`。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONSIM_HTTP_LISTEN` | 空 | 健康检查、CA 下载和 HTTPS 重定向监听地址 |
| `ONSIM_TLS_LISTEN` | 空 | 直接 HTTPS 监听地址 |
| `ONSIM_LISTEN` | `127.0.0.1:8080` | 旧版/单一 HTTP 监听地址 |
| `ONSIM_DATA_DIR` | `./data` | 数据库、录音、密钥和运行状态 |
| `ONSIM_MASTER_KEY` | `<data>/master.key` | 凭据加密主密钥 |
| `ONSIM_GATEWAY_MODE` | 旧模式 | `android`、`sim7600` 或 `mock` |
| `ONSIM_MODEM_MODE` | `auto` | 旧版 Provider 选择；`mock` 仅用于开发 |
| `ONSIM_ANDROID_SERIAL` | 空 | 单手机 ADB 序列号 |
| `ONSIM_ANDROID_GATEWAYS` | 空 | 多 Android 网关 JSON 数组 |
| `ONSIM_ANDROID_SUBSCRIPTION_ID` | `auto` | 旧版单手机订阅选择 |
| `ONSIM_ANDROID_ADB_SERVER_SOCKET` | `tcp:127.0.0.1:5038` | 容器私有 ADB Daemon |
| `ONSIM_ANDROID_CONTROL_ADDR` | `127.0.0.1:47100` | 第一个 Companion 控制转发地址 |
| `ONSIM_ANDROID_AUDIO_ADDR` | `127.0.0.1:47101` | 第一个 Companion 音频转发地址 |
| `ONSIM_AT_PORT` | `/dev/onsim-at` | SIM7600 AT 命令端口 |
| `ONSIM_CONTROL_PORT` | `/dev/onsim-control` | SIM7600 恢复/控制端口 |
| `ONSIM_AUDIO_PORT` | `/dev/onsim-audio` | SIM7600 PCM 端口 |
| `ONSIM_PUBLIC_URL` | `https://onsim.local` | Telegram 通话按钮使用的基础 URL |
| `ONSIM_SESSION_HOURS` | `12` | 管理员会话有效期 |
| `ONSIM_SIP_LISTEN` | `127.0.0.1:5062` | onSIM 本地 PJSIP 监听地址 |
| `ONSIM_SIP_ASTERISK` | `127.0.0.1:5060` | Asterisk Endpoint |
| `ONSIM_SIP_TARGET` | `1001` | 蜂窝来电呼叫的分机 |
| `ONSIM_ASTERISK_CONFIG` | 数据目录下 | 生成的 Asterisk 凭据 Include |

多手机配置示例：

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_GATEWAYS=[{"id":"office-phone","serial":"REPLACE_WITH_FIRST_ADB_SERIAL"},{"id":"backup-phone","serial":"REPLACE_WITH_SECOND_ADB_SERIAL","subscriptionId":2}]
```

功能开关、Telegram Token/Chat ID、SIP 凭据、来电识别 Provider 和过滤规则均在
已认证的「设置」页面管理。返回浏览器的敏感值会被清空；敏感输入留空会保留
已有值。

## 公共 URL 与反向代理

`ONSIM_PUBLIC_URL` 必须是完整的外部 HTTPS Origin，使用非默认端口时也必须包含
端口。反向代理必须保留 WebSocket Upgrade，且不应缓存 API 响应。浏览器需要
可信的安全上下文才能授予麦克风权限。

不要暴露私有 ADB Daemon、Companion 转发端口、Asterisk 控制 Socket、SQLite
文件或录音目录。
