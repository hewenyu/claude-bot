# claude-bot

![Go](https://img.shields.io/badge/go-1.27+-00ADD8?logo=go)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec)](https://linux.do)

**在群里 @ 一下，Claude Code 就去仓库里把活干了。**

把飞书 / Lark、Discord、Telegram、Slack 或钉钉机器人接到项目仓库里常驻的 Claude Code 会话上，GitHub 的 issue 和 PR 也可以作为一个渠道接进来。

项目成员私聊 bot，或在群里 @ 它，提问题、报 bug、提需求。Claude Code 以 `/loop /bot` 的方式运行在仓库里，收到消息后排查、修改、跑测试，然后在原消息下回复。它也盯着仓库的 issue 和 PR。代码改动一律走 PR，实际验证过才合并、发版。

claude-bot 本身只负责**收发消息**：把发给 bot 的消息放进本地收件箱，并提供回复命令。理解需求、决定怎么做、怎么回复，都由 Claude 自己判断。没把 GitHub 接成渠道时，Claude 用 `gh` 自己扫 issue 和 PR。

## 特性

- **五个聊天渠道 + GitHub**：飞书 / Lark、Discord、Telegram、Slack、钉钉，私聊和群里 @bot 都支持；GitHub 的 issue、PR、评论和 review 也能接成渠道。一个仓库里可以同时接多个。
- **不需要公网地址**：全部通过长连接或长轮询从本机主动连出去，不用配域名、HTTPS 和回调地址。
- **一个仓库一个 bot**：凭证、收件箱、监听进程都放在仓库自己的 `.claude-bot/` 里，多个项目可以同时值守、互不影响。
- **几分钟接入**：`claude-bot init` 选渠道。飞书在浏览器里点一下「确认」就建好应用；其他渠道粘贴 token 即可。
- **改代码一律走 PR**：Claude 自己开的 PR 验证后合并、打 tag 发版；别人的 PR 先判断值不值得要，再在本地实际跑过才合并。
- **消息不会丢**：消息一到就写进本地收件箱并加表情回应，会话中断、电脑重启之后接着处理，按顺序串行处理。
- **工具保持简单**：单个 Go 二进制，没有数据库和额外服务。值守流程是一个 Markdown 文件（`.claude/commands/bot.md`），可以按项目约定修改。

```
 私聊 / 群里 @bot、GitHub 动态 ──▶ claude-bot serve ──▶ .claude-bot/inbox/
                                                                     │
 Claude Code (/loop /bot) ◀──── claude-bot inbox --wait ─────────────┘
          └── claude-bot reply <id> ──▶ 原消息下回复
```

详见[工作原理](docs/how-it-works.md)。

## 环境要求

- macOS 或 Linux
- Go 1.27+（用于安装）
- git
- [GitHub CLI](https://cli.github.com)（`gh`，已登录；用来开 PR。把 GitHub 接成渠道后，盯 issue 和 PR 就不再需要它）
- [Claude Code](https://claude.com/claude-code)
- 飞书 / Lark、Discord、Telegram、Slack、钉钉或 GitHub 中任意一个的账号

## 安装

```bash
go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest
```

或者从源码构建：`git clone` 之后 `make build`，生成 `bin/claude-bot`。

## 快速开始

```bash
cd your-project
claude-bot init        # 选渠道、保存凭证、生成 .claude/commands/bot.md、验证收发
claude-bot start       # 在后台启动监听进程（stop 停止，restart 重启）
```

然后在 Claude Code 里挂上 loop：

```
/loop /bot
```

把 bot 拉进项目群，之后大家私聊 bot 或在群里 @ 它就可以了。各渠道怎么建应用、拿 token 见[接入各个渠道](docs/channels.md)。

## 文档

- [接入各个渠道](docs/channels.md)：飞书 / Lark、Discord、Telegram、Slack、钉钉、GitHub 的接入步骤、权限和限制
- [工作原理](docs/how-it-works.md)：消息怎么收进来、多条消息怎么排队处理
- [命令与配置](docs/reference.md)：全部命令和 `init` 参数、项目定位、文件、环境变量、代理
- [常见问题](docs/faq.md)：权限确认卡住、监听进程起不来、升级之后怎么办
- [开发](docs/development.md)：构建、测试和代码结构
- [PROTOCOL.md](PROTOCOL.md)：消息协议，以及怎么接入新渠道

## 社区

本项目在 [LINUX DO](https://linux.do) 社区分享和推广，感谢佬友们的支持与反馈。有问题、有想法，欢迎在 LINUX DO 的帖子里或者 GitHub issue 里提。

[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec?style=for-the-badge)](https://linux.do)

## 许可证

[MIT](LICENSE)。`.claude/skills/` 下的 skill 来自 [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)，沿用其各自声明的 MIT 许可。
