# claude-bot

![Go](https://img.shields.io/badge/go-1.27+-00ADD8?logo=go)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec)](https://linux.do)

**在群里 @ 一下，Claude Code 就去仓库里把活干了。**

把飞书 / Lark、Discord、Telegram 或 Slack 机器人接到项目仓库里常驻的 Claude Code 会话上，GitHub 的 issue 和 PR 也可以作为一个渠道接进来。

项目成员私聊 bot，或在群里 @ 它，提问题、报 bug、提需求。Claude Code 以 `/loop /bot` 的方式运行在仓库里，收到消息后排查、修改、跑测试，然后在原消息下回复。它也盯着仓库的 issue 和 PR：值得做的就修复、实现或在本地验证后合并，然后打 tag 发版；不值得做的也会回复说明理由。

claude-bot 本身只负责**收发消息**：把发给 bot 的消息放进本地收件箱，并提供回复命令。理解需求、决定怎么做、怎么回复，都由 Claude 自己判断。没把 GitHub 接成渠道时，Claude 用 `gh` 自己扫 issue 和 PR。

## 特性

- **四个聊天渠道**：飞书 / Lark、Discord、Telegram、Slack，一个仓库里可以同时接多个。私聊和群里 @bot 都支持，回复会回在原消息下（或原话题、thread 里），群聊里会 @ 提问的人。
- **不需要公网地址**：全部通过长连接或长轮询从本机主动连出去，家里的电脑、公司内网都能用，不用配域名、HTTPS 和回调地址。
- **一个仓库一个 bot**：凭证、收件箱、监听进程都放在仓库自己的 `.claude-bot/` 里，多个项目可以同时值守、互不影响。
- **几分钟接入**：`claude-bot init` 选渠道。飞书在浏览器里点一下「确认」就会自动建好应用、配好权限；其他渠道粘贴 token 即可。最后发一条私聊验证收发都已打通。
- **聊天之外还管 GitHub**：盯新开的 issue 和 PR，值得做的修复后合并、打 tag 发版，PR 要在本地实际跑过才会合并；不值得做的也会回复理由。默认由 Claude 用 `gh` 每轮扫一遍；也可以把 GitHub 接成渠道，用 token 把 issue、PR、评论和 review 直接收进收件箱，和聊天消息一起排队处理。
- **消息不会丢**：消息一到就写进本地收件箱并加表情回应，会话中断、电脑重启之后接着处理。按顺序串行处理，同一时间只有一个任务在改代码。
- **工具保持简单**：单个 Go 二进制，只负责收发消息，没有数据库，也不需要额外的服务。值守流程是一个 Markdown 文件（`.claude/commands/bot.md`），可以按项目约定修改。

## 工作原理

```
 私聊 bot / 群里 @bot                 ┌──▶ 给原消息加表情：收到了
 （飞书、Discord、Telegram、Slack）    │
 GitHub 的 issue、PR、评论、review     │
        ──长连接 / 轮询──▶ claude-bot serve ──▶ .claude-bot/inbox/
                                                         │
 Claude Code (/loop /bot) ◀──── claude-bot inbox --wait ─┘
          │
          └── claude-bot reply <id> ──▶ 原消息下回复（群里 @ 发送者，GitHub 上发评论）
```

- **怎么连出去**：飞书和 Discord 用长连接（WebSocket），Slack 用 Socket Mode，Telegram 用长轮询，GitHub 每 30 秒轮询一次 REST API，都是从本机主动发起的。
- **监听进程按需启动**：第一次执行 `claude-bot inbox` 时，会在后台自动拉起 `serve`。之后用 `init` 新接了渠道，下一次 `inbox` 会发现正在运行的监听进程没有监听这个渠道，自动把它重启。

### 多条消息怎么处理

一个仓库只有一个 Claude Code 会话在值守，消息是**按顺序一条一条处理**的：

1. **到了就落盘**：监听进程收到消息后，马上写进 `.claude-bot/inbox/pending/`（一条一个文件，平台重复投递会去重），并给原消息加一个表情（飞书是 OnIt，其他渠道是 👀），发消息的人立刻知道 bot 已经收到。
2. **先到先处理**：每一轮 `claude-bot inbox` 会取出所有待处理的消息，最早的排在前面。Claude 逐条排查、修改、测试、回复。回复之后，这条消息才移到 `inbox/done/`。
3. **处理中新来的消息排在后面**：在收件箱里等着，当前这批处理完后的下一轮再取。
4. **不会丢**：Claude Code 会话中断、电脑重启都不影响，没有回复的消息一直留在 `pending/` 里，重新 `/loop /bot` 之后接着处理。

串行的好处是同一时间只有一个任务在改代码，提交和发版不会互相冲突。代价是排在后面的人要等前面的任务做完，比如前面是一个要改半小时代码的 bug，后面一个一句话就能回答的问题也要等。遇到耗时长的任务，Claude 会先用 `reply --keep` 回一句「收到，正在看」，做完再正式回复。

想知道还有多少条在排队，可以运行 `claude-bot status`，看 `pending` 字段。

协议细节和接入新渠道的方法见 [PROTOCOL.md](PROTOCOL.md)。

## 环境要求

- macOS 或 Linux
- Go 1.27+（用于安装）
- git
- [GitHub CLI](https://cli.github.com)（`gh`，已登录；把 GitHub 接成渠道后，盯 issue 和 PR 就不再需要它）
- [Claude Code](https://claude.com/claude-code)
- 飞书 / Lark、Discord、Telegram、Slack 或 GitHub 中任意一个的账号

## 安装

```bash
go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest
```

或者从源码构建：

```bash
git clone https://github.com/hewenyu/claude-bot.git
cd claude-bot
make build        # 生成 bin/claude-bot
```

## 快速开始

**1. 在项目仓库里初始化**

```bash
cd your-project
claude-bot init
```

`init` 会先让你选渠道（飞书 / Lark、Discord、Telegram、Slack、GitHub）。至少接一个，其他的之后再运行 `claude-bot init --channel <渠道>` 补上，多个渠道可以同时用。选 Discord、Telegram、Slack 或 GitHub 时，它会提示你输入 token（输入时不显示），获取方法见下面各渠道的说明。不在终端里运行时（比如脚本，或者 Claude Code 的 Bash 工具），不会弹出菜单，默认接飞书；要接其他渠道，用 `--channel` 或对应的 token 参数指定。

不管选哪个渠道，`init` 都会把凭证写入 `.claude-bot/config.json`（权限 `0600`），把 `.claude-bot/` 加进 `.gitignore`，生成值守流程 `.claude/commands/bot.md`（已经存在就保留），最后等你私聊 bot 一条消息，收到「连接成功」的回复就说明收发都已打通（GitHub 没有私聊，只检查 token 能不能访问仓库）。

`.claude/commands/bot.md` 是值守流程（处理聊天消息和 GitHub 的 issue、PR），可以按项目约定修改。

#### 选飞书时

`init` 会打开浏览器，走飞书官方的应用注册授权流程。登录后点「确认」，就会创建好应用，并配置好机器人能力、[权限](#权限)和事件订阅（长连接），不用去开放平台手动配置。已经有应用的话，用 `--app-id` 和 `--app-secret` 接入。

#### 选 Discord 时

1. 在 [Discord Developer Portal](https://discord.com/developers/applications) 新建一个应用，在 Bot 页面点 Reset Token 拿到 token。
2. 在 `init` 的菜单里选 Discord 后粘贴 token，或者直接运行：

   ```bash
   claude-bot init --discord-token <token>
   ```

   它会验证 token，把它写进 `.claude-bot/config.json`，打印把 bot 邀请进服务器的链接（已带上所需权限），然后等你私聊 bot 来验证连接。

不需要开启 Message Content 这个特权 intent：私聊和 @ 了 bot 的消息，Discord 总会给出正文，而 bot 只处理这两种。在服务器里要 @ bot 本人；@ 它的同名角色不会被投递。

#### 选 Telegram 时

1. 在 Telegram 里找 [@BotFather](https://t.me/BotFather)，发 `/newbot` 建一个 bot，拿到 token。每个项目建一个。
2. 建议关掉隐私模式：给 @BotFather 发 `/setprivacy`，选这个 bot，再选 `Disable`。开着隐私模式时，群里 @bot 的普通消息可能收不到。关掉后 bot 也只处理 @ 它或回复它的消息。如果 bot 已经在群里，要移出群再拉回来才生效。
3. 在 `init` 的菜单里选 Telegram 后粘贴 token，或者直接运行：

   ```bash
   claude-bot init --telegram-token <token>
   ```

   它会验证 token，写进 `.claude-bot/config.json`，然后等你私聊 bot 来验证连接。之后把 bot 拉进群即可，话题（topic）群也支持。

Telegram 的 Bot API 没法按 ID 读取一条消息，所以 `get` 在 Telegram 上不可用。作为替代，回复某条消息时，被回复的内容会以引用（`> 名字: …`）的形式放在 `text` 开头。国内访问 Telegram 需要代理，默认使用环境变量里的代理，见[代理](#代理)。

#### 选 Slack 时

1. 在 [Slack API](https://api.slack.com/apps) 点 Create New App → From a manifest，选工作区，粘贴下面的 manifest（把名字改成项目名）。它会开启 Socket Mode，所以同样不需要公网地址：

   ```yaml
   display_information:
     name: myproject Bot
   features:
     app_home:
       messages_tab_enabled: true
       messages_tab_read_only_enabled: false
     bot_user:
       display_name: myproject Bot
       always_online: true
   oauth_config:
     scopes:
       bot:
         - app_mentions:read
         - chat:write
         - im:history
         - channels:history
         - groups:history
         - mpim:history
         - files:read
         - reactions:write
   settings:
     event_subscriptions:
       bot_events:
         - app_mention
         - message.im
     socket_mode_enabled: true
     token_rotation_enabled: false
   ```

2. 在 Basic Information → App-Level Tokens 里生成一个带 `connections:write` 的 token（`xapp-` 开头）；在 Install App 里装到工作区，拿到 Bot User OAuth Token（`xoxb-` 开头）。
3. 在 `init` 的菜单里选 Slack 后依次粘贴两个 token，或者直接运行：

   ```bash
   claude-bot init --slack-app-token xapp-… --slack-bot-token xoxb-…
   ```

   它会验证两个 token，写进 `.claude-bot/config.json`，然后等你私信 bot 来验证连接。之后在频道里 `/invite @bot`。

在频道里 @bot 的消息，回复会发在这条消息的 thread 里，并 @ 提问的人；私信里直接回复。回复里的 `<!channel>`、`<!here>` 这类全员提醒不会生效。

#### 选 GitHub 时

接了 GitHub 渠道之后，Claude 不用每轮跑 `gh` 去扫仓库：新开的 issue 和 PR、issue 和 PR 下的评论、PR 的 review 和行内 review 评论，都会进收件箱（只收人发的，Actions、Dependabot 这类 bot 的不收）。Claude 回复时会在原 issue 或 PR 下发评论并 @ 对方，行内 review 评论回在同一个讨论串里。同样不需要公网地址和 webhook：监听进程每 30 秒轮询一次 REST API，没有新动态时 GitHub 返回 304，不占限流额度。

1. 建一个 token。推荐 [fine-grained token](https://github.com/settings/personal-access-tokens/new)，只选这个仓库，权限：
   - Issues：读写
   - Pull requests：读写
   - Contents：读写（合并 PR 要用，只回复的话可以只读）
   - Metadata：只读（默认就有）
2. 在 `init` 的菜单里选 GitHub 后粘贴 token，或者直接运行：

   ```bash
   claude-bot init --github-token <token>
   ```

   仓库默认从 `origin` 远程地址读出来，不是 GitHub 地址时用 `--github-repo <owner>/<name>` 指定。它会检查 token 能不能访问这个仓库、有没有写权限。

用谁的 token，回复就显示成谁发的。可以单独注册一个机器人账号，也可以直接用自己的。用自己的也没关系：bot 发的评论末尾都带一个隐藏标记 `<!-- claude-bot -->`，只有带标记的才会被跳过，你自己手写的评论照样会进收件箱。

监听进程第一次启动时，会把还开着、而且最后一条评论不是 bot 回复的 issue 和 PR 放进收件箱；已经处理过（回复过或者 `done` 过）的不会再进来。

回复之外的操作用 `claude-bot github`：

```bash
claude-bot github close <id> [--not-planned]               # 关 issue 或 PR
claude-bot github merge <id> [--method squash|merge|rebase] # 合并 PR，默认 squash
claude-bot github review <id> approve|request-changes|comment [text|-]
```

`<id>` 是收件箱里的 `github:<owner>/<repo>/<编号>`，带 `#c…`（评论）、`#r…`（行内 review 评论）、`#v…`（review）后缀的也可以，会作用在所属的 issue 或 PR 上。

**2. 在 Claude Code 里挂上 loop**

```
/loop /bot
```

**3. 把 bot 拉进项目群**

之后大家私聊 bot 或在群里 @ 它就可以了。

## 命令

```
claude-bot [-C <repo>] <command> [args]
```

| 命令 | 说明 |
|---|---|
| `init` | 创建或接入 bot 应用，保存凭证，生成 `/bot` 命令，验证连接 |
| `inbox [--wait 9m]` | 以 JSON Lines 输出待处理消息；监听进程没在运行时自动启动 |
| `reply [--keep] <id> [text\|-]` | 在原消息下回复（Markdown），并标记为已处理 |
| `done <id>...` | 标记为已处理，不回复 |
| `get <id>` | 获取任意一条消息，比如 `reply_to` 指向的那条 |
| `download <id>` | 下载消息附件到 `.claude-bot/files/<消息>/`，输出文件路径 |
| `send <channel>:<chat_id> [text\|-]` | 主动发消息到某个会话；GitHub 上是在 `github:<owner>/<repo>/<编号>` 下发评论 |
| `github close\|merge\|review <id> …` | 关 issue 或 PR、合并 PR、提交 review，见[选 GitHub 时](#选-github-时) |
| `serve` | 在前台运行监听进程 |
| `status` | 查看 bot 身份、监听进程状态和待处理消息数 |
| `version` | 查看版本 |

文本参数可以写在命令行上，也可以用 `-` 或省略，改为从 stdin 读取，适合多行内容配合 heredoc 使用。退出码：`0` 成功，`1` 失败，`2` 用法错误。

`init` 的常用参数：

| 参数 | 说明 |
|---|---|
| `--channel lark\|discord\|telegram\|slack\|github` | 直接指定渠道，不弹菜单；没给 token 参数时在终端里提示输入 |
| `--bot-md` | 只用当前版本覆盖 `.claude/commands/bot.md`，不动任何配置。升级 claude-bot 后用它更新值守流程 |
| `--app-id`、`--app-secret` | 使用已有应用，不新建 |
| `--discord-token` | 接入 Discord bot |
| `--telegram-token` | 接入 Telegram bot |
| `--slack-app-token`、`--slack-bot-token` | 接入 Slack 应用，两个一起用 |
| `--github-token`、`--github-repo` | 接入 GitHub 渠道；仓库默认取 `origin` |
| `--domain feishu\|lark` | 配合 `--app-id` 使用，指定国内版或国际版，默认 `feishu` |
| `--proxy direct\|env\|<url>` | 网络代理，见[代理](#代理) |
| `--name` | bot 名称，默认 `<仓库名> Bot` |
| `--reregister` | 即使已经配置过，也重新创建应用 |
| `--no-open` | 只打印授权链接，不自动打开浏览器 |
| `--no-verify` | 跳过发消息的连接验证 |
| `--force` | 接入渠道的同时覆盖已有的 `.claude/commands/bot.md` |

## 配置

### 项目定位

项目根目录按以下顺序确定：

1. `-C <dir>` 指定的目录（会解析到它所在的 git 仓库根目录）
2. 当前目录所在的 git 仓库
3. `$CLAUDE_PROJECT_DIR`（Claude Code 会话的根目录）
4. 当前目录

在包含多个仓库的父目录里运行时，用 `-C` 指定仓库，比如 `claude-bot -C api status`；对应的 loop 写成 `/loop /bot api`。

### 文件

```
.claude/commands/bot.md      值守流程（init 生成，提交进仓库，团队共享）
.claude-bot/                 已加入 .gitignore
  config.json                凭证，0600
  inbox/pending/             待处理消息，每条一个 JSON 文件
  inbox/done/                已处理消息
  files/                     下载的附件
  serve.json                 监听进程心跳
  serve.log                  监听进程日志
  serve.lock                 单实例锁
```

### 环境变量

| 变量 | 说明 |
|---|---|
| `LARK_APP_ID`、`LARK_APP_SECRET` | 不使用 `init`，直接用环境变量提供凭证（两个都设置时覆盖 `config.json`） |
| `LARK_DOMAIN` | 设为 `lark` 表示国际版，默认是飞书 |
| `DISCORD_BOT_TOKEN` | 不使用 `init`，直接用环境变量提供 Discord bot token（覆盖 `config.json`） |
| `TELEGRAM_BOT_TOKEN` | 同上，Telegram bot token |
| `SLACK_APP_TOKEN`、`SLACK_BOT_TOKEN` | 同上，Slack 的两个 token（两个都设置时生效） |

### 代理

飞书默认直连，不读取 `HTTPS_PROXY`，因为很多本地代理会让到 `open.feishu.cn` 的 TLS 连接失败。Lark 国际版、Discord、Telegram、Slack 和 GitHub 默认使用环境变量里的代理。可以用 `init --proxy env` 或 `--proxy http://host:port` 修改。

### 权限

飞书：`init` 会为应用申请以下权限：

| 权限 | 用途 |
|---|---|
| `im:message` | 读取和发送消息 |
| `im:message.p2p_msg:readonly` | 接收私聊消息 |
| `im:message.group_at_msg:readonly` | 接收群里 @bot 的消息 |
| `im:message:send_as_bot` | 以 bot 身份发消息 |
| `im:resource` | 下载图片和文件 |
| `im:message.reactions:write_only` | 用表情回应"已收到" |

权限需要变多时，再运行一次 `claude-bot init`，它会对同一个应用补充授权，不会新建应用。如果企业需要管理员审批，`init` 会列出尚未生效的权限。

Discord：邀请链接会申请这几项权限：查看频道、发消息、在子区（thread）里发消息、读取消息历史、添加表情回应。

Telegram：不需要额外申请权限。bot 收到消息后会用 👀 表情回应，群里关掉了表情回应的话这一步会静默失败，不影响收发。

Slack：manifest 里的 scope 依次用于：收 @bot 的消息、发消息、收私信、用 `get` 读取频道 / 私有频道 / 群聊私信里的消息、下载附件、用 👀 表情回应。

## 常见问题

**无人值守时被权限确认卡住**

在项目的 `.claude/settings.json` 里放行 bot 的命令：

```json
{ "permissions": { "allow": ["Bash(claude-bot:*)"] } }
```

**init 卡在浏览器那一步**

授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开，手机也可以。加 `--no-open` 则只打印链接。

**监听进程起不来 / 连不上**

运行 `claude-bot status` 查看状态，日志在 `.claude-bot/serve.log`。监听进程启动时如果 token 无效（比如已被撤销），会直接退出，并在日志里写明原因，重新 `init` 一次即可。本机开了代理的情况见[代理](#代理)。

**升级 claude-bot 之后**

`go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest` 之后，运行 `claude-bot init --bot-md` 用新版本的值守流程覆盖 `.claude/commands/bot.md`（不动任何配置），再重启 `/loop /bot`。如果你改过 bot.md，先备份或者用 git 看一下差异。正在运行的监听进程还是旧版本，可以结束它（`claude-bot status` 里有 pid），下次 `inbox` 会自动用新版本拉起。

**群里一次来了好几个任务**

按到达顺序逐个处理，每条消息到达时都会先收到一个表情回应，见[多条消息怎么处理](#多条消息怎么处理)。

## 开发

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
internal/channel/github/    GitHub 适配器（REST API 轮询 issue、PR、评论和 review；close / merge / review 操作）
internal/markdown/          各适配器共用的 Markdown 拆分
internal/inbox/             基于文件的收件箱
internal/config/            项目定位和凭证加载
internal/transport/         各适配器共用的 HTTP / WebSocket 客户端、代理、限长下载、重连退避
internal/atomicfile/        原子写文件
```

各适配器只依赖 `channel`、`transport`、`markdown` 这几个公共包，互相之间没有依赖；CLI 收发消息只通过 `channel.Channel` 接口，用到具体渠道类型的只有按配置创建适配器（`app.go`）和 `init`、`status`、`github` 这几个命令。接入新的平台只需要实现 `channel.Channel` 接口，详见 [PROTOCOL.md §3](PROTOCOL.md#3-渠道适配器)。

`.claude/skills/` 下装了 Go 最佳实践相关的 skill（[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)），在这个仓库里开发时 Claude 会参照这些规范。

## 社区

本项目在 [LINUX DO](https://linux.do) 社区分享和推广，感谢佬友们的支持与反馈。有问题、有想法，欢迎在 LINUX DO 的帖子里或者 GitHub issue 里提。

[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec?style=for-the-badge)](https://linux.do)

## 许可证

[MIT](LICENSE)。`.claude/skills/` 下的 skill 来自 [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)，沿用其各自声明的 MIT 许可。
