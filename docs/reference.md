# 命令与配置

## 命令

```
claude-bot [-C <repo>] <command> [args]
```

| 命令 | 说明 |
|---|---|
| `init` | 创建或接入 bot 应用，保存凭证，生成 `/bot` 命令，验证连接 |
| `inbox [--wait 9m]` | 以 JSON Lines 输出待处理消息；监听进程没在运行时报错 |
| `reply [--keep] <id> [text\|-]` | 在原消息下回复（Markdown），并标记为已处理 |
| `done <id>...` | 标记为已处理，不回复 |
| `get <id>` | 获取任意一条消息，比如 `reply_to` 指向的那条 |
| `download <id>` | 下载消息附件到 `.claude-bot/files/<消息>/`，输出文件路径 |
| `send <channel>:<chat_id> [text\|-]` | 主动发消息到某个会话；GitHub 上是在 `github:<owner>/<repo>/<编号>` 下发评论 |
| `github close\|merge\|review <id> …` | 关 issue 或 PR、合并 PR、提交 review，见[渠道接入 · GitHub](channels.md#github) |
| `start` | 在后台启动监听进程，等它连上所有渠道后返回 |
| `stop` | 停止监听进程 |
| `restart` | 重启监听进程，升级 claude-bot 或新接了渠道之后用 |
| `serve` | 在前台运行监听进程 |
| `status` | 查看 bot 身份、监听进程状态和待处理消息数 |
| `version` | 查看版本 |

文本参数可以写在命令行上，也可以用 `-` 或省略，改为从 stdin 读取，适合多行内容配合 heredoc 使用。退出码：`0` 成功，`1` 失败，`2` 用法错误。

`init` 的常用参数：

| 参数 | 说明 |
|---|---|
| `--channel lark\|discord\|telegram\|slack\|github\|dingtalk` | 直接指定渠道，不弹菜单；没给 token 参数时在终端里提示输入 |
| `--bot-md` | 只用当前版本覆盖 `.claude/commands/bot.md`，不动任何配置。升级 claude-bot 后用它更新值守流程 |
| `--pi` | 只安装 pi 的 `/loop` 扩展（`.pi/extensions/claude-bot-loop.ts`），不动任何配置，见[用其他 agent 值守](agents.md#pi) |
| `--app-id`、`--app-secret` | 使用已有应用，不新建 |
| `--discord-token` | 接入 Discord bot |
| `--telegram-token` | 接入 Telegram bot |
| `--slack-app-token`、`--slack-bot-token` | 接入 Slack 应用，两个一起用 |
| `--dingtalk-client-id`、`--dingtalk-client-secret` | 接入钉钉应用的机器人，两个一起用 |
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
.agents/skills/bot/SKILL.md  指向 bot.md 的符号链接，给读 Agent Skills 的 agent（比如 pi）用
.pi/extensions/              pi 的 /loop 扩展（init --pi 安装）
.claude-bot/                 已加入 .gitignore
  config.json                凭证，0600
  inbox/pending/             待处理消息，每条一个 JSON 文件
  inbox/done/                已处理消息
  files/                     下载的附件
  serve.json                 监听进程心跳
  serve.log                  监听进程日志
  serve.lock                 单实例锁，里面是监听进程的 pid
```

### 环境变量

| 变量 | 说明 |
|---|---|
| `LARK_APP_ID`、`LARK_APP_SECRET` | 不使用 `init`，直接用环境变量提供凭证（两个都设置时覆盖 `config.json`） |
| `LARK_DOMAIN` | 设为 `lark` 表示国际版，默认是飞书 |
| `DISCORD_BOT_TOKEN` | 不使用 `init`，直接用环境变量提供 Discord bot token（覆盖 `config.json`） |
| `TELEGRAM_BOT_TOKEN` | 同上，Telegram bot token |
| `SLACK_APP_TOKEN`、`SLACK_BOT_TOKEN` | 同上，Slack 的两个 token（两个都设置时生效） |
| `DINGTALK_CLIENT_ID`、`DINGTALK_CLIENT_SECRET` | 同上，钉钉应用的 Client ID 和 Client Secret（两个都设置时生效） |

环境变量只替换凭证，`config.json` 里同一渠道的代理、域名等其他设置照样生效。`init` 只读写 `config.json`，不会把环境变量里的凭证写进去。

### 代理

飞书和钉钉默认直连，不读取 `HTTPS_PROXY`，因为很多本地代理会让到国内服务（比如 `open.feishu.cn`）的 TLS 连接失败。Lark 国际版、Discord、Telegram、Slack 和 GitHub 默认使用环境变量里的代理。可以用 `init --proxy env` 或 `--proxy http://host:port` 修改。
