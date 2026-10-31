# 工作原理

```
 私聊 bot / 群里 @bot                  ┌──▶ 给原消息加表情：收到了
 （飞书、Discord、Telegram、Slack、    │
   钉钉）                              │
 GitHub 的 issue、PR、评论、review     │
        ──长连接 / 轮询──▶ claude-bot gateway ──▶ 各项目的 .claude-bot/inbox/
                                                         │
 Claude Code (/loop /bot) ◀──── claude-bot inbox --wait ─┘
          │
          └── claude-bot reply <task:id> ──▶ 原消息下回复（群里 @ 发送者，GitHub 上发评论）
```

- **怎么连出去**：飞书和 Discord 用长连接（WebSocket），Slack 用 Socket Mode，钉钉用 Stream 模式，Telegram 用长轮询，GitHub 每 30 秒轮询一次 REST API，都是从本机主动发起的。
- **一个 gateway 服务所有项目**：每个项目有自己的 bot，一个 bot 可以接多个渠道；一个应用只属于一个项目（同一个应用连两次，飞书、钉钉、Slack 会随机分走消息，Telegram 互相抢，Discord 各回一遍），登记时就会拒绝。gateway 在一个进程里给每个登记过的项目连上它的渠道、用 Jev 筛选、把事情写进这个项目自己的 `.claude-bot/inbox/`，并在那里写心跳；各项目的 `/loop /bot`、`inbox`、`reply` 和以前单独跑监听进程时完全一样。某个项目或某个渠道出错只停它自己。
- **配置在本地 SQLite 里**：`~/.claude-bot/gateway.db`（权限 0600，不加密）存着登记的项目、各项目的渠道凭证，以及所有项目共用的 TypeSafe key 和 GitHub token（默认一个给所有项目用；属于另一个账号的项目可以用一个有名字的 token）。`init` 改了配置，gateway 几秒内自动用上，不用重启。对话和附件仍是各项目 `.claude-bot/` 下的 JSON 文件和普通文件。
- **每个项目一个 Claude Code**：gateway 只管收消息、写进各项目自己的收件箱。每个项目各开一个 Claude Code 跑 `/loop /bot`，它在项目里运行的 `inbox --wait`、`reply`、`done` 只读写这个项目的 `.claude-bot/`，回复也用这个项目自己的应用发出去，不会串到别的项目。
- **本地网页只是看和改配置**：gateway 在本机回环地址上提供一个网页（`claude-bot gateway open`），读的就是数据库、各项目的心跳和收件箱里的 JSON；在网页上改渠道等于改数据库，gateway 照常几秒内重连。网页不参与处理消息，关掉或者端口被占用都不影响值守。
- **gateway 由你启停**：在自己的终端里运行 `claude-bot gateway start`，它在后台启动，连上各项目后列出它们的状态；`claude-bot gateway stop` 停止，`claude-bot gateway restart` 重启（升级之后用）。这样它用的是你终端里的环境变量（比如代理设置），不会继承 Claude Code 会话里的。`inbox` 不会自己启动它：没在运行时直接报错，版本比当前命令旧时提示你 `claude-bot gateway restart`。
- **还没迁进 gateway 的项目**（用以前版本 `init` 的，配置在 `.claude-bot/config.json`）照旧用 `claude-bot start` / `stop` / `restart` 跑它自己的监听进程。gateway 不会去连一个自己的监听进程还在跑的项目，等它停掉才接手。
- **空闲时不花 token**：每一轮处理完，Claude 在后台运行 `claude-bot inbox --wait 55m`，然后停下来。来了消息它马上退出，Claude Code 随即把会话叫醒处理，延迟在几秒内；一直没消息，大约每小时醒一次，看看监听进程是否正常。

## 多条消息怎么处理

一个仓库只有一个 Claude Code 会话在值守，处理的单位是**一件事**，不是一条消息：

1. **到了就落盘**：监听进程收到消息后，马上写进 `.claude-bot/inbox/incoming/`（一条一个文件，平台重复投递会去重），并给原消息加一个表情（飞书是 OnIt，其他渠道是 👀），发消息的人立刻知道 bot 已经收到。
2. **Jev 归成事**：等这个会话停顿几秒，Jev 按「是不是同一件事」把这批消息归组（一条消息连同附件总是整条归进一件事，不拆开），再判断每组是不是在接着这个会话里还没做完、或者 24 小时内做过的某件事说（补充、追问、回答 bot 的问题，群里别人说的同一件事也算），是就并进去，不是就开一件新的。非法的直接回复「现在暂时不支持。」，不交给 Claude。每件事存成一个文件，带着整段对话，包括 bot 之前的回复。
3. **一件事一个子 Agent，串行**：每一轮 `claude-bot inbox` 取出所有待处理的事，等得最久的在前。主会话只做调度：一次开一个子 Agent，只把这一件事交给它，它处理完、回复、返回一句话结果，再开下一件。不同的人、不同的事各有各的上下文，互不干扰。
4. **处理中又来的消息**：同一件事的补充会并回这件事；子 Agent 回复时只算处理了它看到的部分，新补充的让这件事留在待处理里，下一轮再取。别的事排在后面。
5. **不会丢**：Claude Code 会话中断、电脑重启都不影响，没处理完的事一直留在 `inbox/tasks/pending/` 里，重新 `/loop /bot` 之后接着处理。

串行的好处是同一时间只有一个任务在改代码，提交和发版不会互相冲突。代价是排在后面的人要等前面的任务做完，比如前面是一个要改半小时代码的 bug，后面一个一句话就能回答的问题也要等。遇到耗时长的任务，Claude 会先用 `reply --keep` 回一句「收到，正在看」，做完再正式回复。

想知道还有多少件事在排队，可以运行 `claude-bot status`，看 `pending` 字段。

协议细节和接入新渠道的方法见 [PROTOCOL.md](../PROTOCOL.md)。
