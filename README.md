# claude-bot

让 Claude Code 以 `/loop` 的形式常驻在项目仓库里，当这个项目的值守 bot。项目成员可以私聊 bot，也可以在群里 @ 它，提问题、报 bug、提需求；Claude 收到后在仓库里排查、修复，然后在原消息下回复。

目前支持**飞书 / Lark**。其他渠道（比如 Discord）按 [PROTOCOL.md](PROTOCOL.md) 实现一个适配器就能接入。GitHub 相关操作 Claude 直接用 `gh`。

## 安装

```bash
go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest
```

## 接入一个项目

在项目仓库里运行：

```bash
cd your-project
claude-bot init
```

`init` 会做这几件事：

1. **自动创建飞书应用**：打开浏览器，登录飞书后点「确认」就行。机器人能力、权限（收私聊和群里 @bot 的消息、回复、下载附件、表情回应）、事件订阅（长连接）都会一次配好。用的是飞书官方的应用注册授权流程，不需要去开放平台手动配置。
2. **保存凭证**：写到这个仓库的 `.claude-bot/config.json`（权限 0600），并自动把 `.claude-bot/` 加进 `.gitignore`。
3. **生成值守流程**：生成 `.claude/commands/bot.md`。可以按项目需要修改，比如加上"发版用 `make release`"之类的约定。
4. **连接验证**：提示你在飞书里给 bot 发一条私聊，bot 回复"连接成功"，就说明收发都打通了。

然后打开 Claude Code，挂上 loop：

```
/loop /bot
```

把 bot 拉进项目群，大家私聊它或在群里 @ 它就行。

已经有应用的话，也可以直接用：`claude-bot init --app-id cli_xxx --app-secret xxx`。如果之后需要的权限变多了，再运行一次 `claude-bot init`，它会对同一个应用补授权，不会新建应用。

## 多个项目、多个 bot

配置跟着仓库走：每个仓库有自己的 `.claude-bot/`，里面放凭证、收件箱和监听进程，都不进 git。一个仓库对应一个 bot，多个项目可以同时值守，互不干扰。

- 在仓库里（包括子目录）直接运行，会自动定位到仓库根目录。
- 在包含多个仓库的父目录里，用 `-C` 指定仓库：`claude-bot -C herdr-agent status`，loop 写成 `/loop /bot herdr-agent`。
- 同一个仓库可以同时接多个平台：`config.json` 按平台分段（目前是 `lark`，以后加 `discord`），一个监听进程会同时处理所有已配置的平台。

## 常见问题

- **无人值守时被权限确认卡住**：在项目的 `.claude/settings.json` 里放行 bot 的命令：
  ```json
  { "permissions": { "allow": ["Bash(claude-bot:*)"] } }
  ```
- **连不上飞书 / 本机开了代理**：飞书默认直连，不走 `HTTPS_PROXY`，因为很多本地代理会把 `open.feishu.cn` 的 TLS 连接搞坏。如果确实需要走代理，用 `init --proxy env` 或 `--proxy http://host:port`。Lark 国际版默认沿用环境变量里的代理。
- **看状态 / 看日志**：运行 `claude-bot status`，日志在 `.claude-bot/serve.log`。
- **不用 init，用环境变量配置**：设置 `LARK_APP_ID`、`LARK_APP_SECRET`，可选设置 `LARK_DOMAIN=lark`。
- **init 卡在浏览器那一步**：授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开（也可以用手机）。加 `--no-open` 就只打印链接、不自动打开浏览器。

## 开发

```bash
make test   # go test -race
make lint   # golangci-lint（配置来自 samber/cc-skills-golang 的 golang-lint）
make build
```

`.claude/skills/` 下装了 Go 最佳实践相关的 skill（[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)），在这个仓库里开发时 Claude 会参照这些规范。
