# 工作原理

```
 私聊 bot / 群里 @bot                  ┌──▶ 给原消息加表情：收到了
 （飞书、Discord、Telegram、Slack、    │
   钉钉）                              │
 GitHub 的 issue、PR、评论、review     │
        ──长连接 / 轮询──▶ claude-bot serve ──▶ .claude-bot/inbox/
                                                         │
 Claude Code (/loop /bot) ◀──── claude-bot inbox --wait ─┘
          │
          └── claude-bot reply <id> ──▶ 原消息下回复（群里 @ 发送者，GitHub 上发评论）
```

- **怎么连出去**：飞书和 Discord 用长连接（WebSocket），Slack 用 Socket Mode，钉钉用 Stream 模式，Telegram 用长轮询，GitHub 每 30 秒轮询一次 REST API，都是从本机主动发起的。
- **监听进程由你启停**：在自己的终端里运行 `claude-bot start`，它会在后台启动 `serve`，等所有渠道都连上后返回；`claude-bot stop` 停止，`claude-bot restart` 重启。这样监听进程用的是你终端里的环境变量（比如代理设置），不会继承 Claude Code 会话里的。`inbox` 不会自己启动监听进程：监听进程没在运行时直接报错；正在运行的监听进程版本比当前命令旧，或者没有监听新接的渠道时，`inbox` 会提示你 `claude-bot restart`。
- **空闲时不花 token**：每一轮处理完，Claude 在后台运行 `claude-bot inbox --wait 55m`，然后停下来。来了消息它马上退出，Claude Code 随即把会话叫醒处理，延迟在几秒内；一直没消息，大约每小时醒一次，看看监听进程是否正常。

## 多条消息怎么处理

一个仓库只有一个 Claude Code 会话在值守，消息是**按顺序一条一条处理**的：

1. **到了就落盘**：监听进程收到消息后，马上写进 `.claude-bot/inbox/pending/`（一条一个文件，平台重复投递会去重），并给原消息加一个表情（飞书是 OnIt，其他渠道是 👀），发消息的人立刻知道 bot 已经收到。
2. **先到先处理**：每一轮 `claude-bot inbox` 会取出所有待处理的消息，最早的排在前面。Claude 逐条排查、修改、测试、回复。回复之后，这条消息才移到 `inbox/done/`。
3. **处理中新来的消息排在后面**：在收件箱里等着，当前这批处理完后的下一轮再取。
4. **不会丢**：Claude Code 会话中断、电脑重启都不影响，没有回复的消息一直留在 `pending/` 里，重新 `/loop /bot` 之后接着处理。

串行的好处是同一时间只有一个任务在改代码，提交和发版不会互相冲突。代价是排在后面的人要等前面的任务做完，比如前面是一个要改半小时代码的 bug，后面一个一句话就能回答的问题也要等。遇到耗时长的任务，Claude 会先用 `reply --keep` 回一句「收到，正在看」，做完再正式回复。

想知道还有多少条在排队，可以运行 `claude-bot status`，看 `pending` 字段。

协议细节和接入新渠道的方法见 [PROTOCOL.md](../PROTOCOL.md)。
