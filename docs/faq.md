# 常见问题

**无人值守时被权限确认卡住**

在项目的 `.claude/settings.json` 里放行 bot 的命令：

```json
{ "permissions": { "allow": ["Bash(claude-bot:*)"] } }
```

**init 卡在浏览器那一步**

授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开，手机也可以。加 `--no-open` 则只打印链接。

**监听进程起不来 / 连不上**

运行 `claude-bot status` 查看状态，日志在 `.claude-bot/serve.log`。监听进程启动时如果 token 无效（比如已被撤销），会直接退出，并在日志里写明原因，重新 `init` 一次即可。本机开了代理的情况见[代理](reference.md#代理)。

**升级 claude-bot 之后**

`go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest` 之后，运行 `claude-bot init --bot-md` 用新版本的值守流程覆盖 `.claude/commands/bot.md`（不动任何配置），再重启 `/loop /bot`。如果你改过 bot.md，先备份或者用 git 看一下差异。正在运行的监听进程还是旧版本，可以结束它（`claude-bot status` 里有 pid），下次 `inbox` 会自动用新版本拉起。

**群里一次来了好几个任务**

按到达顺序逐个处理，每条消息到达时都会先收到一个表情回应，见[多条消息怎么处理](how-it-works.md#多条消息怎么处理)。
