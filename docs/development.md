# 开发

```bash
make test     # go test -race ./...
make lint     # golangci-lint run
make fmt      # golangci-lint fmt
make build    # bin/claude-bot
```

代码结构：

```
cmd/claude-bot/             CLI 入口和各个命令（init 按渠道拆在 init_*.go 里）
internal/channel/           渠道协议：消息格式、Channel 接口、id 和下载的公共函数
internal/channel/lark/      飞书 / Lark 适配器、应用注册流程
internal/channel/discord/   Discord 适配器（Gateway 长连接）
internal/channel/telegram/  Telegram 适配器（Bot API 长轮询）
internal/channel/slack/     Slack 适配器（Socket Mode）
internal/channel/dingtalk/  钉钉适配器（Stream 模式）
internal/channel/github/    GitHub 适配器（REST API 轮询 issue、PR、评论和 review；close / merge / review 操作）
internal/markdown/          各适配器共用的 Markdown 拆分
internal/inbox/             基于文件的收件箱
internal/jev/               TypeSafe Jev 的最小客户端（只问是非题）
internal/screen/            用 Jev 把待处理消息裁剪归组、拒绝非法请求
internal/config/            项目定位和凭证加载
internal/transport/         各适配器共用的 HTTP / WebSocket 客户端、代理、WebSocket 保活、限长下载、重连退避
internal/atomicfile/        原子写文件
```

各适配器只依赖 `channel`、`transport`、`markdown` 这几个公共包，互相之间没有依赖；CLI 收发消息只通过 `channel.Channel` 接口，用到具体渠道类型的只有按配置创建适配器（`app.go`）和 `init`、`status`、`github` 这几个命令。接入新的平台只需要实现 `channel.Channel` 接口，详见 [PROTOCOL.md §3](../PROTOCOL.md#3-渠道适配器)。

`internal/screen` 的效果用真实的 Jev 测（用本仓库 `.claude-bot/config.json` 里的 TypeSafe key，花 token，不在 `make test` 里跑）：

```bash
JEV_LIVE=1 go test -run Live -v ./internal/screen
```

会输出带标注的对话归组对了多少、非法请求拒了多少、正常请求误拒了多少；有误拒就失败。改切分规则、问题措辞或阈值后都要跑一遍。

`.claude/skills/` 下装了 Go 最佳实践相关的 skill（[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)），在这个仓库里开发时 Claude 会参照这些规范。
