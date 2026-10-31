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
- **一个 gateway 管所有项目**：本机只跑一个 `claude-bot gateway` 进程，服务所有登记过的项目。每个项目有自己的 bot（一个应用只属于一个项目），可以同时接多个渠道。配置统一存在本地 SQLite（`~/.claude-bot/gateway.db`），TypeSafe key 和 GitHub token 只配一次；消息仍进各项目自己的 `.claude-bot/inbox/`，多个项目同时值守、互不影响。
- **本地网页看全局**：gateway 自带一个只在本机打开的网页（`claude-bot gateway open`）：每个项目和它的渠道连没连上、出了什么错；每个项目处理过哪些事，点开看整段对话和 bot 的回复；在页面上添加、修改渠道凭证并测试连接，改完 gateway 几秒内自动重连。凭证只进不出，页面上只显示末几位。
- **几分钟接入**：`claude-bot init` 选渠道。飞书在浏览器里点一下「确认」就建好应用；其他渠道粘贴 token 即可。
- **改代码一律走 PR**：Claude 自己开的 PR 验证后合并、打 tag 发版；别人的 PR 先判断值不值得要，再在本地实际跑过才合并。
- **消息不会丢**：消息一到就写进本地收件箱并加表情回应，会话中断、电脑重启之后接着处理，按顺序串行处理。
- **Jev 先过一遍**：每条消息先由 [TypeSafe](https://typesafe.ai) 的 Jev 模型筛选：把消息和附件归组成一件件事（一条消息不会拆开），追问（24 小时内）并回原来那件事，附件下载好并标明存在哪里；非法请求直接回复「现在暂时不支持。」，不交给 Claude。Claude 一次开一个子 Agent 处理一件事，带着这件事的整段对话，不同人的事上下文互不干扰。`init` 时填入 TypeSafe API key。见 [PROTOCOL.md §1.1](PROTOCOL.md#11-jev-筛选)。
- **工具保持简单**：单个 Go 二进制，数据库是一个本地 SQLite 文件，网页也打包在二进制里，没有额外的服务要装。值守流程是一个 Markdown 文件（`.claude/commands/bot.md`），可以按项目约定修改。

```
 私聊 / 群里 @bot、GitHub 动态 ──▶ claude-bot gateway ──▶ 各项目的 .claude-bot/inbox/
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
claude-bot init           # 填 TypeSafe API key、选渠道、把项目登记进 gateway、生成 .claude/commands/bot.md、验证收发
claude-bot gateway start  # 在后台启动 gateway，一个进程服务所有项目（stop 停止，restart 重启）
claude-bot gateway open   # 在浏览器里打开 gateway 的网页，看各项目的状态和处理过的事
```

然后在 Claude Code 里挂上 loop：

```
/loop /bot
```

把 bot 拉进项目群，之后大家私聊 bot 或在群里 @ 它就可以了。再值守一个项目，只要在那个仓库里 `claude-bot init`，gateway 几秒内自动接上，不用重启。

以前用 `claude-bot start` 单独跑监听进程的项目，升级后运行 `claude-bot gateway import` 迁进 gateway，见[常见问题](docs/faq.md#从单项目监听进程迁到-gateway)。各渠道怎么建应用、拿 token 见[接入各个渠道](docs/channels.md)。

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
