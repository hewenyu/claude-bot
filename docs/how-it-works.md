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
- **每个项目一个 Claude Code**：gateway 收消息、写进各项目自己的收件箱。每个项目一个 Claude Code 跑 `/bot`：打开了「自动处理」的由 gateway 启动（见[自动处理](#自动处理)），没打开的自己开终端挂 `/loop /bot`。它它在项目里运行的 `inbox --wait`、`reply`、`done` 只读写这个项目的 `.claude-bot/`，回复也用这个项目自己的应用发出去，不会串到别的项目。
- **本地网页看全局、改配置**：gateway 在本机回环地址上提供一个网页（`claude-bot gateway open`），读的就是数据库、各项目的心跳和收件箱里的 JSON；在网页上改渠道等于改数据库，gateway 照常几秒内重连。网页不参与处理消息，关掉不影响值守；端口被占用时网页起不来，「自动处理」也跟着停（gateway 启动的 Claude 要靠这个端口报告状态），自己开的 `/loop /bot` 照常。
- **gateway 由你启停**：在自己的终端里运行 `claude-bot gateway start`，它在后台启动，连上各项目后列出它们的状态；`claude-bot gateway stop` 停止，`claude-bot gateway restart` 重启（升级之后用）。这样它用的是你终端里的环境变量（比如代理设置），不会继承 Claude Code 会话里的。`inbox` 不会自己启动它：没在运行时直接报错，版本比当前命令旧时提示你 `claude-bot gateway restart`。
- **还没迁进 gateway 的项目**（用以前版本 `init` 的，配置在 `.claude-bot/config.json`）照旧用 `claude-bot start` / `stop` / `restart` 跑它自己的监听进程。gateway 不会去连一个自己的监听进程还在跑的项目，等它停掉才接手。
- **空闲时不花 token**：每一轮处理完，Claude 在后台运行 `claude-bot inbox --wait 55m`，然后停下来。来了消息它马上退出，Claude Code 随即把会话叫醒处理，延迟在几秒内；一直没消息，大约每小时醒一次，看看监听进程是否正常。

## 多条消息怎么处理

一个仓库只有一个 Claude Code 会话在值守，处理的单位是**一件事**，不是一条消息：

1. **到了就落盘**：监听进程收到消息后，马上写进 `.claude-bot/inbox/incoming/`（一条一个文件，平台重复投递会去重），并给原消息加一个表情（飞书是 OnIt，其他渠道是 👀），发消息的人立刻知道 bot 已经收到。
2. **Jev 归成事**：等这个会话停顿几秒，Jev 按「是不是同一件事」把这批消息归组（一条消息连同附件总是整条归进一件事，不拆开），再判断每组是不是在接着这个会话里还没做完、或者 24 小时内做过的某件事说（补充、追问、回答 bot 的问题，群里别人说的同一件事也算），是就并进去，不是就开一件新的。非法的直接回复「现在暂时不支持。」，不交给 Claude。每件事存成一个文件，带着整段对话，包括 bot 之前的回复。
3. **整批取出，主会话主导**：每一轮 `claude-bot inbox` 取出所有待处理的事，等得最久的在前。Claude 先把这一批看完，再按轻重缓急处理：一句话能答的先回，要改代码的按急的先来，几件能一起改的放进同一个 PR、只发一个版本，再用各自的 `id` 分别回复。要不要开子 Agent 帮忙（并行改几件不相干的事、排查会撑满上下文的大问题）由 Claude 自己判断，回复、开 PR、发版仍由主会话来做。
4. **处理中又来的消息**：同一件事的补充会并回这件事；回复时只算处理了 Claude 看到的部分，新补充的让这件事留在待处理里，下一轮再取。别的事排在后面。
5. **不会丢**：Claude Code 会话中断、电脑重启都不影响，没处理完的事一直留在 `inbox/tasks/pending/` 里，重新 `/loop /bot` 之后接着处理。

只有一个会话在改代码，提交和发版不会互相冲突；整批处理让一句话就能回答的问题不用排在半小时的 bug 后面，几个 issue 一个 PR、一次发版。遇到耗时长的任务，Claude 会先用 `reply --keep` 回一句「收到，正在看」，做完再正式回复。验证不过、要等人确认的那件会从这一批的 PR 里拿掉，不拖住其他的。

## 自动处理

不想每个仓库都开着一个终端挂 `/loop /bot`，可以让 gateway 来跑。在网页上进项目的「自动处理」标签，打开开关：

1. **有待办才启动**：gateway 每几秒看一眼收件箱，有待办就在项目目录里启动 Claude Code，给它 `/bot`。怎么取、先做哪件、怎么回，都按项目的 `.claude/commands/bot.md` 来，gateway 不管。项目里（或 `~/.claude/commands/`）没有 `/bot` 命令时不会启动，提示你运行 `claude-bot init --bot-md`。
2. **一轮完了它自己等**：和 `/loop /bot` 一样，`/bot` 每一轮最后在后台运行 `inbox --wait`，来了新消息它自己醒来接着处理，gateway 不插手。来了消息它两分钟都没醒（比如后台等待意外退出了），gateway 再给它一次 `/bot`。
3. **名额**：每个项目最多一个 Claude，所有项目一共最多 3 个（在「共用设置」里改，1 到 32）。名额满了还有项目在排队，空闲最久的那个 Claude 让出名额：gateway 结束它，有新消息时再接着原来的会话启动（`--resume`），之前做到哪、说过什么都还在。
4. **退出了不乱重启**：Claude 自己退出、被你结束、或者让出名额之后，gateway 记下收件箱里当时还剩什么；只有新的事或者新的追问才会让它再启动，剩下的那些等 30 分钟后再跑一轮。启动就失败的（比如没登录）不会每几秒重来一次。
5. **gateway 重启**：它启动的 Claude 跟着结束；gateway 起来后，正在处理的接着原来的会话恢复（连续中断三次就停下，等你看），空闲的等新消息。
6. **需要你的时候**：Claude 停在确认框上（比如第一次进一个目录时问是否信任它）、或者你按了 Esc 中断了这一轮，标签上会显示「等你确认」「需要你看看」。终端就在页面上，可以直接看、直接打字回答；处理完点「再跑一轮」，或者等新消息。

gateway 启动的 Claude 默认带 `--permission-mode bypassPermissions`：没人守在旁边一个个点确认，所以项目要放在给它用的机器上。想换成别的权限模式或者加参数，启动 gateway 前设置 `CLAUDE_BOT_RUNNER_ARGS`（见[环境变量](reference.md#环境变量)）。

**安全**：这些 Claude 以你的身份运行、跳过权限确认，能读写你能读写的一切，包括 `~/.claude-bot/gateway.db` 里的渠道凭证和 token。只给信任的项目打开「自动处理」；项目的 `bot.md` 就是给它的指令，改它要像改代码一样审。

**同一个项目别两种方式一起用**：打开了「自动处理」，就不要再在那个项目里自己开 `/loop /bot`，不然两个 Claude 会抢同一批任务。

**桌面 App**：`desktop/` 是这个网页的独立窗口（Electron），Apple Silicon 的 Mac 可以从 Releases 下载 DMG（见 [README · 安装](../README.md#安装)）。App 里带着 `claude-bot`，它在你的登录 shell 里用这个 `claude-bot` 运行 `gateway start`（所以 gateway 和它启动的 Claude 用的是你终端里的 PATH 和环境变量），再用 `claude-bot gateway open --print` 拿到带令牌的地址打开；页面外的链接交给浏览器。关掉窗口不影响 gateway 和它在跑的 Claude。从源码运行：`make desktop`。

想知道还有多少件事在排队，可以运行 `claude-bot status`，看 `pending` 字段。

协议细节和接入新渠道的方法见 [PROTOCOL.md](../PROTOCOL.md)。
