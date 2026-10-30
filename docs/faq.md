# 常见问题

**无人值守时被权限确认卡住**

在项目的 `.claude/settings.json` 里放行 bot 的命令：

```json
{ "permissions": { "allow": ["Bash(claude-bot:*)"] } }
```

**init 卡在浏览器那一步**

授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开；终端下方还有它的二维码，用飞书 App 扫一下就行（终端宽度不足时会提示拉宽窗口）。加 `--no-open` 则只打印链接和二维码，不打开浏览器。

**监听进程起不来 / 连不上**

运行 `claude-bot status` 查看状态，日志在 `.claude-bot/serve.log`。某个渠道出现致命错误（比如 token 无效或已被撤销）时，只停掉这一个渠道，其他渠道继续运行：日志里会有一行 `channel stopped` 写明原因，`claude-bot status` 的 `listener.stopped` 会列出它，`claude-bot inbox` 也会提示；重新 `init` 这个渠道后 `claude-bot restart` 即可。所有渠道都停了，监听进程才会退出。本机开了代理的情况见[代理](reference.md#代理)。

**升级 claude-bot 之后**

`go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest` 之后：

1. 运行 `claude-bot restart`，换成新版本的监听进程。
2. 运行 `claude-bot init --bot-md`，用新版本的值守流程覆盖 `.claude/commands/bot.md`（不动任何配置）。如果你改过 bot.md，先备份，或者用 git 看一下差异。
3. 重启 `/loop /bot`。

**inbox 报 listener not running**

监听进程要你自己在终端里运行 `claude-bot start` 来启动，Claude 不会替你启动，免得监听进程带上 Claude Code 会话里的环境变量。电脑重启之后也要重新 start。

**群里一次来了好几个任务**

按到达顺序逐个处理，每条消息到达时都会先收到一个表情回应，见[多条消息怎么处理](how-it-works.md#多条消息怎么处理)。
