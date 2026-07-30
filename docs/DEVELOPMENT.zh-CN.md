# 开发与测试

[English](DEVELOPMENT.md)

## 原生依赖

```sh
sudo apt-get install -y golang-go nodejs npm gcc pkg-config \
  libsqlite3-dev libopus-dev libopusfile-dev opus-tools
```

## 构建与测试

```sh
make web
make build
make test
```

`make web` 会生成 `webui/dist`，该目录有意不提交。生产 Containerfile 会执行
前端构建并运行全部 Go 测试。

## Mock 模式

```sh
ONSIM_GATEWAY_MODE=mock \
ONSIM_MODEM_MODE=mock \
ONSIM_LISTEN=127.0.0.1:8080 \
ONSIM_DATA_DIR="$(mktemp -d)" \
go run ./cmd/onsim
```

使用 `+8613800138000` 等虚构测试号码。测试绝不能连接真实运营商、Telegram
聊天或生产数据库。

## 前端

```sh
cd web
npm ci
npm run dev
npm run build
```

Vite 会将 API 请求代理到本地后端。需要同时验证窄屏手机视口和桌面布局。安装
PWA 需要生产构建和安全上下文（localhost 也符合要求）。

## 测试要求

- Go Package：`go test -tags libsqlite3 ./...`
- 前端：`npm run build`（包含 Vue/TypeScript 检查）
- 格式：`gofmt` 和 `git diff --check`
- Provider 行为：使用 Mock/Fake Transport，不拨打真实电话或发送短信
- Telegram：使用本地 Mock Bot API
- 媒体：使用确定性的 PCM Frame 和状态转换

硬件验收测试独立于自动化测试，并且必须使用运维人员拥有的号码/账号。

## Pull Request

将 Provider 特有行为放在网关抽象之后。协议变更、状态转换、重连处理和迁移都
应添加测试。生产日志必须删除电话号码、SIM/设备标识符和 Token 后才能提交。
