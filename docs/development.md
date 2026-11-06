# 开发

```bash
make test     # go test -race ./...
make lint     # golangci-lint run
make fmt      # golangci-lint fmt
make build    # bin/claude-bot
make web     # 用 web/ 重新构建 gateway 的网页到 internal/webui/static（要 Node）
make web-check  # 重新构建，确认提交的 internal/webui/static 和 web/ 一致
make desktop  # 用 bin/claude-bot 运行桌面 App（要 Node）
make dmg      # 在 Apple Silicon 的 Mac 上构建 desktop/dist/claude-bot-<版本>-arm64.dmg（ad hoc 签名，未公证）
```

CI 和发版都在 Kiln（维护者自己的 CI）上跑：

- `.kiln/workflows/ci.yml`：每个 PR 和 master 上，Mac mini 跑 lint、race tests、核对提交的网页和桌面 App 的类型检查，linux/arm64 再用 `CGO_ENABLED=0`（和发布的二进制一样）跑一遍测试。
- `.kiln/workflows/release.yml`：推送 `v*` tag 后，在 Mac mini 上构建 DMG，在 darwin/arm64、linux/arm64、linux/amd64 上各自编译二进制并运行一次核对版本，一起发布到 GitHub Release（另附 `SHA256SUMS`）。PR 上也构建一遍，但不发布。发版前先确认 master 的 ci 是绿的。
- 改了 Go 或 Node 的版本（`go.mod`、`.node-version`）后，用 Kiln 的 `kiln runtime lock` 重新生成 `.kiln/runtimes.lock`，`kiln lint` 检查 workflow。

代码结构：

```
cmd/claude-bot/             CLI 入口和各个命令，按关注点分文件：
  main.go                     命令表，用法和分发都从它来；args.go 参数解析；app.go 打开项目
  init*.go                    接入：init_choose 选渠道，init_lark 飞书建应用，init_token 其他渠道
  serve.go screen.go closed.go  监听：收消息、Jev 筛选、平台上已经关掉的事
  messages.go status.go github.go  给 Claude 的命令：inbox / reply / done / get / download / send，status，github
  gateway*.go                 gateway 本身、它的命令和 status；gateway_runner.go 是「自动处理」
  gateway_web*.go             网页的接口：_http 鉴权和应答，其余按任务、渠道、设置、GitHub token、runner 分
  hook.go                     gateway 启动的 Claude 用的 hook 和 finish
  daemon.go heartbeat.go layout.go  后台进程、心跳、.claude-bot/ 的布局
internal/channel/           渠道协议：消息格式、Channel 接口、id、名字缓存和下载的公共函数
internal/channel/lark/      飞书 / Lark 适配器、应用注册流程
internal/channel/discord/   Discord 适配器（Gateway 长连接）
internal/channel/telegram/  Telegram 适配器（Bot API 长轮询）
internal/channel/slack/     Slack 适配器（Socket Mode）
internal/channel/dingtalk/  钉钉适配器（Stream 模式）
internal/channel/github/    GitHub 适配器（REST API 轮询 issue、PR、评论和 review；close / merge / review 操作）
internal/markdown/          各适配器共用的 Markdown 拆分
internal/inbox/             基于文件的收件箱：待筛选的消息、事情（task）
internal/jev/               TypeSafe Jev 的最小客户端（只问是非题）
internal/screen/            用 Jev 把新消息归组（一条消息不拆开）、接上以前的事、拒绝非法请求
internal/config/            项目定位，配置的读写（gateway 数据库，或者以前的 config.json）和迁移；kinds.go 是渠道表
internal/store/             gateway 的 SQLite 数据库（纯 Go 的 modernc.org/sqlite，不用 cgo）：共用设置、登记的项目、各项目的渠道
internal/transport/         各适配器共用的 HTTP / WebSocket 客户端、代理、WebSocket 保活、流式下载、重连退避
internal/atomicfile/        原子写文件、读写 JSON，以及各处共用的文件权限
internal/filelock/          内核持有的文件锁，进程死了自动释放（收件箱、单实例锁）
internal/runner/            「自动处理」的调度：每个项目什么时候启动、关掉它的 Claude，名额怎么分（做什么由 /bot 决定）
internal/agent/             在伪终端里运行 Claude Code，通过 hook、终端标题和屏幕判断它在做什么；跟踪并关掉它的整个进程家族
internal/term/              终端输出的环形缓冲、标题解析，以及替还没连上的终端回答终端查询
internal/webui/             gateway 网页的静态文件（go:embed），由 web/ 构建后提交，所以 go install 不需要 Node
web/                        gateway 网页的源码：React + TypeScript + Vite；各渠道在页面上的名字、后台链接、文档锚点在 web/src/channelKinds.ts
desktop/                    桌面 App：网页的 Electron 窗口，确保 gateway 在跑
```

改网页：改 `web/src/` 后运行 `make web`，把 `internal/webui/static/` 的变化一起提交。开发时可以起一个假数据的 gateway（临时目录、假渠道，每种状态的项目都有，不连任何平台）：

```bash
CLAUDE_BOT_PAGE_DEMO=127.0.0.1:7601 go test -run TestPageDemo -timeout 0 ./cmd/claude-bot
# 打开 http://127.0.0.1:7601/#token=de30de30
```

要热更新，把 `web/vite.config.ts` 里代理的端口改成 7601，在 `web/` 里 `npm run dev`。

各适配器只依赖 `channel`、`transport`、`markdown` 这几个公共包，互相之间没有依赖；CLI 收发消息只通过 `channel.Channel` 接口；用到具体渠道类型的只有 `internal/config/kinds.go` 的渠道表，和 `init`、`status`、`github`、网页的渠道表单这几处。接入新的平台只需要实现 `channel.Channel` 接口，详见 [PROTOCOL.md §3](../PROTOCOL.md#3-渠道适配器)。

`internal/screen` 的效果用真实的 Jev 测（用本仓库配置里的 TypeSafe key，花 token，不在 `make test` 里跑）：

```bash
JEV_LIVE=1 go test -run Live -v ./internal/screen
```

会输出带标注的对话归组对了多少、追问并回原来那件事的对了多少、非法请求拒了多少、正常请求误拒了多少；有误拒、或者新的事被并进了旧的事，就失败。改切分规则、问题措辞或阈值后都要跑一遍。

`.claude/skills/` 下装了 Go 最佳实践相关的 skill（[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)），在这个仓库里开发时 Claude 会参照这些规范。
