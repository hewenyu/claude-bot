# claude-bot

![Go](https://img.shields.io/badge/go-1.27+-00ADD8?logo=go)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

把飞书 / Lark 机器人接到项目仓库里常驻的 Claude Code 会话上。

项目成员私聊 bot，或在群里 @ 它，提问题、报 bug、提需求。Claude Code 以 `/loop /bot` 的方式运行在仓库里，收到消息后排查、修改、跑测试，然后在原消息下回复。

claude-bot 本身只负责**收发消息**：把发给 bot 的消息放进本地收件箱，并提供回复命令。理解需求、决定怎么做、怎么回复，都由 Claude 自己判断。GitHub 相关操作 Claude 直接用 `gh`。

## 工作原理

```
 飞书私聊 / 群里 @bot ──长连接──▶ claude-bot serve ──▶ .claude-bot/inbox/
                                                           │
 Claude Code (/loop /bot) ◀──── claude-bot inbox --wait ───┘
          │
          └── claude-bot reply <id> ──▶ 飞书（原消息下回复，群里 @ 发送者）
```

- **不需要公网地址**：通过飞书的长连接（WebSocket）接收事件。
- **一个仓库一个 bot**：凭证、收件箱、监听进程都放在仓库自己的 `.claude-bot/` 里，多个项目可以同时值守。
- **监听进程按需启动**：第一次执行 `claude-bot inbox` 时，会在后台自动拉起 `serve`。

协议细节和接入新渠道的方法见 [PROTOCOL.md](PROTOCOL.md)。

## 环境要求

- macOS 或 Linux
- Go 1.27+（用于安装）
- git
- [Claude Code](https://claude.com/claude-code)
- 飞书或 Lark 账号

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

`init` 会：

1. 打开浏览器，走飞书官方的应用注册授权流程。登录后点「确认」，就会创建好应用，并配置好机器人能力、[权限](#权限)和事件订阅（长连接），不用去开放平台手动配置。
2. 把凭证写入 `.claude-bot/config.json`（权限 `0600`），并把 `.claude-bot/` 加进 `.gitignore`。
3. 生成值守流程 `.claude/commands/bot.md`，可以按项目约定修改。
4. 验证连接：在飞书里给 bot 发一条私聊，收到「连接成功」的回复就说明收发都已打通。

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
| `send <channel>:<chat_id> [text\|-]` | 主动发消息到某个会话 |
| `serve` | 在前台运行监听进程 |
| `status` | 查看 bot 身份、监听进程状态和待处理消息数 |
| `version` | 查看版本 |

文本参数可以写在命令行上，也可以用 `-` 或省略，改为从 stdin 读取，适合多行内容配合 heredoc 使用。退出码：`0` 成功，`1` 失败，`2` 用法错误。

`init` 的常用参数：

| 参数 | 说明 |
|---|---|
| `--app-id`、`--app-secret` | 使用已有应用，不新建 |
| `--domain feishu\|lark` | 配合 `--app-id` 使用，指定国内版或国际版，默认 `feishu` |
| `--proxy direct\|env\|<url>` | 网络代理，见[代理](#代理) |
| `--name` | bot 名称，默认 `<仓库名> Bot` |
| `--reregister` | 即使已经配置过，也重新创建应用 |
| `--no-open` | 只打印授权链接，不自动打开浏览器 |
| `--no-verify` | 跳过发消息的连接验证 |
| `--force` | 覆盖已有的 `.claude/commands/bot.md` |

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

### 代理

飞书默认直连，不读取 `HTTPS_PROXY`，因为很多本地代理会让到 `open.feishu.cn` 的 TLS 连接失败。Lark 国际版默认使用环境变量里的代理。可以用 `init --proxy env` 或 `--proxy http://host:port` 修改。

### 权限

`init` 会为应用申请以下权限：

| 权限 | 用途 |
|---|---|
| `im:message` | 读取和发送消息 |
| `im:message.p2p_msg:readonly` | 接收私聊消息 |
| `im:message.group_at_msg:readonly` | 接收群里 @bot 的消息 |
| `im:message:send_as_bot` | 以 bot 身份发消息 |
| `im:resource` | 下载图片和文件 |
| `im:message.reactions:write_only` | 用表情回应"已收到" |

权限需要变多时，再运行一次 `claude-bot init`，它会对同一个应用补充授权，不会新建应用。如果企业需要管理员审批，`init` 会列出尚未生效的权限。

## 常见问题

**无人值守时被权限确认卡住**

在项目的 `.claude/settings.json` 里放行 bot 的命令：

```json
{ "permissions": { "allow": ["Bash(claude-bot:*)"] } }
```

**init 卡在浏览器那一步**

授权链接也会打印在终端里，可以复制到任何已登录飞书的浏览器里打开，手机也可以。加 `--no-open` 则只打印链接。

**监听进程起不来 / 连不上飞书**

运行 `claude-bot status` 查看状态，日志在 `.claude-bot/serve.log`。本机开了代理的情况见[代理](#代理)。

## 开发

```bash
make test     # go test -race ./...
make lint     # golangci-lint run
make fmt      # golangci-lint fmt
make build    # bin/claude-bot
```

代码结构：

```
cmd/claude-bot/          CLI 入口和各个命令
internal/channel/        渠道协议：消息格式和 Channel 接口
internal/channel/lark/   飞书 / Lark 适配器、应用注册流程
internal/inbox/          基于文件的收件箱
internal/config/         项目定位和凭证加载
internal/atomicfile/     原子写文件
```

接入新的聊天平台（比如 Discord）只需要实现 `channel.Channel` 接口，详见 [PROTOCOL.md §3](PROTOCOL.md#3-渠道适配器)。

`.claude/skills/` 下装了 Go 最佳实践相关的 skill（[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)），在这个仓库里开发时 Claude 会参照这些规范。

## 许可证

[MIT](LICENSE)。`.claude/skills/` 下的 skill 来自 [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)，沿用其各自声明的 MIT 许可。
