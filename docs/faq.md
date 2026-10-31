# 常见问题

**无人值守时被权限确认卡住**

在项目的 `.claude/settings.json` 里放行 bot 的命令：

```json
{ "permissions": { "allow": ["Bash(claude-bot:*)"] } }
```

**init 卡在浏览器那一步**

授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开；终端下方还有它的二维码，用飞书 App 扫一下就行（终端宽度不足时会提示拉宽窗口）。加 `--no-open` 则只打印链接和二维码，不打开浏览器。

**把链接发给别人去建飞书应用，init 一直「等待确认」**

飞书只把凭证交给 init 打印出来的那个链接：确认页打开后会把链接里的 `user_code` 从地址栏去掉，从浏览器地址栏复制的地址仍能建出应用（对方页面显示创建成功），但这个应用不会传回 init，init 会一直等到链接过期。要让别人确认，用 `claude-bot init --no-open` 运行，把终端里打印的链接或二维码原样发给对方，自己先别打开。已经建好的应用，请对方在飞书开放平台该应用的「凭证与基础信息」里复制 App ID 和 App Secret，用 `claude-bot init --app-id <App ID> --app-secret <App Secret>` 接入（国际版加 `--domain lark`），或者在 gateway 页面里添加飞书渠道填进去。init 等待期间每两分钟会提示一次剩余时间和这个办法。

**监听进程起不来 / 连不上**

运行 `claude-bot gateway status` 查看每个项目的状态和出错原因，日志在 `~/.claude-bot/gateway.log`（还没迁进 gateway 的项目看 `claude-bot status` 和 `.claude-bot/serve.log`）。某个渠道出现致命错误（比如 token 无效或已被撤销）时，只停掉这一个渠道，其他渠道和其他项目继续运行：日志里会有一行 `channel stopped` 写明原因，`gateway status` 的 `stopped` 和 `errors` 会列出它，`claude-bot inbox` 也会提示；重新 `init` 这个渠道即可，gateway 几秒内自动用上新配置。项目显示 `busy` 时，说明它自己的监听进程还在跑，按提示在项目里运行 `claude-bot stop`。本机开了代理的情况见[代理](reference.md#代理)。

**升级 claude-bot 之后**

`go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest` 之后：

1. 运行 `claude-bot gateway restart`，换成新版本的 gateway（还没迁进 gateway 的项目运行 `claude-bot restart`）。
2. 运行 `claude-bot init --bot-md`，用新版本的值守流程覆盖 `.claude/commands/bot.md`（不动任何配置）。如果你改过 bot.md，先备份，或者用 git 看一下差异。
3. 重启 `/loop /bot`。

**inbox 报 listener not running**

gateway 要你自己在终端里运行 `claude-bot gateway start` 来启动（还没迁进 gateway 的项目是 `claude-bot start`），Claude 不会替你启动，免得它带上 Claude Code 会话里的环境变量。电脑重启之后也要重新 start。

**从单项目监听进程迁到 gateway**

以前每个项目用 `claude-bot start` 跑自己的监听进程、配置在 `.claude-bot/config.json`。升级 claude-bot 之后，在每个项目里：

```bash
claude-bot gateway import   # 配置迁进 ~/.claude-bot/gateway.db，config.json 改名为 config.json.imported
claude-bot stop             # 停掉这个项目自己的监听进程
```

然后运行一次 `claude-bot gateway start`。顺序无所谓：gateway 不会去连一个自己的监听进程还在跑的项目（那样同一个应用会有两个连接，消息会被随机分走），而是标成 `busy`，等它停掉，几秒内自动接手。`/loop /bot` 不用重启。

一个应用只属于一个项目：两个项目填了同一个飞书应用（或同一个钉钉应用、Telegram bot、GitHub 仓库……），后登记的那个会报错，说明被哪个项目占着。

要退回单项目模式，运行 `claude-bot gateway remove <项目名>`，配置会写回这个项目的 `.claude-bot/config.json`。

**群里一次来了好几个任务**

按到达顺序逐个处理，每条消息到达时都会先收到一个表情回应，见[多条消息怎么处理](how-it-works.md#多条消息怎么处理)。
