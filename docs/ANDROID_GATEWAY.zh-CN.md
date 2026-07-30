# Android 网关

[English](ANDROID_GATEWAY.md)

Android Provider 使用一台专用手机作为蜂窝网络网关。控制信令和 16 kHz PCM
音频通过私有 ADB 转发传输。Companion 会成为默认电话与短信应用，并需要系统
特权权限。

## 重要警告

- 解锁 Bootloader 通常会清除手机上的全部数据。
- 刷入错误的 Boot 镜像可能导致手机无法启动。
- 必须在目标手机上使用官方 Magisk 应用修补同一版本的 Boot 镜像。
- 保存原厂 Boot 镜像及其哈希值，并提前验证恢复流程。
- 切勿提交 OTA、Boot 镜像、APK 签名密钥、ADB 密钥或 Token。
- 普通的非 Root Android 应用无法采集和注入蜂窝通话音频。

项目提供的脚本针对已验证的一加 5 Android 10 分区布局。其他设备应按移植项目
处理，不能直接套用安装流程。

## 1. 确认准确的系统版本

```sh
adb devices -l
export ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
adb -s "$ONSIM_ANDROID_SERIAL" shell getprop ro.build.fingerprint
adb -s "$ONSIM_ANDROID_SERIAL" shell getprop gsm.version.baseband
```

从合法来源取得完全匹配的原厂 OTA 并验证校验和。切勿使用与系统指纹不匹配的
OTA 或 `boot.img`。

```sh
export ONSIM_ONEPLUS_ROM=/absolute/path/to/matching-stock-ota.zip
scripts/prepare-oneplus5.sh
```

生成的固件材料保存在 `data/` 下，并已被 Git 忽略。

## 2. 构建 Companion

```sh
scripts/build-android-gateway.sh
```

构建产物写入 `dist/android/`。首次构建会在 `data/android-signing/` 下生成
私有签名密钥。请安全备份；后续升级必须使用同一签名身份。

## 3. 配置 Magisk

在目标手机上使用官方 Magisk 应用修补提取出的原厂 Boot 镜像。拉取修补结果，
先验证临时启动，再永久刷入：

```sh
scripts/root-oneplus5.sh manager
scripts/root-oneplus5.sh pull
scripts/root-oneplus5.sh test
scripts/root-oneplus5.sh flash
```

仔细阅读每条提示，不要跳过临时启动验证。恢复已保存的原厂镜像：

```sh
scripts/recover-oneplus5.sh
```

## 4. 初始化设备

```sh
scripts/provision-oneplus5.sh
```

该脚本会安装已签名的 Companion 和 Magisk 模块，配置私有控制 Token，分配电话/
短信角色，并启用特权服务。

重启后检查：

```sh
adb -s "$ONSIM_ANDROID_SERIAL" shell su -c id
adb -s "$ONSIM_ANDROID_SERIAL" shell dumpsys telecom
adb -s "$ONSIM_ANDROID_SERIAL" shell dumpsys role
```

## 5. 配置 onSIM

编辑 `~/.config/onsim/onsim.env`：

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
ONSIM_ANDROID_SUBSCRIPTION_ID=auto
```

重启并检查：

```sh
systemctl --user restart onsim.service
podman exec onsim adb -P 5038 devices -l
podman exec onsim adb -P 5038 forward --list
```

## 双卡与多手机

单台手机只有一个就绪的订阅时，`auto` 会自动选择它。存在多个活动订阅时，在
Web UI 中选择手机/SIM。

多台手机使用 JSON 配置：

```dotenv
ONSIM_ANDROID_GATEWAYS=[{"id":"phone-a","serial":"REPLACE_WITH_FIRST_ADB_SERIAL"},{"id":"phone-b","serial":"REPLACE_WITH_SECOND_ADB_SERIAL"}]
```

每个 `id` 和 `serial` 必须唯一。未指定地址时，onSIM 会为各网关分配独立的
控制和音频转发端口。

## 验收清单

- 手机和容器重启后 Companion 能自动重连。
- 「信息」页面显示预期的 SIM 和运营商。
- 运营商支持时，通话期间 IMS/VoLTE 保持注册。
- 收发短信在断线重连后仍正常，且不会产生重复事件。
- 呼入和呼出通话的双向音频清晰。
- 保持、恢复、DTMF、静音、远端挂断和语音信箱均正常。
- 锁屏不会停止网关服务。

音频健康状态必须反映真实的 Telephony Tx/Rx 路由，不能仅凭 Android
`AudioTrack` 创建成功就判断正常。
