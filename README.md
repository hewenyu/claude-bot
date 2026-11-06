<img src="docs/logo.svg" width="64" height="64" alt="claude-bot">

# claude-bot

![Go](https://img.shields.io/badge/go-1.27+-00ADD8?logo=go)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec)](https://linux.do)

**在群里 @ 一下，Claude Code 就去仓库里把活干了。**

项目成员私聊 bot，或在群里 @ 它，提问题、报 bug、提需求；仓库里有人开 issue、提 PR、写评论。本机的 claude-bot gateway 把这些收进项目的收件箱，有待办时在仓库里启动 Claude Code 跑 `/bot`：它排查、改代码、跑测试、开 PR，然后在原消息下回复。一批处理完，这个 Claude Code 就关掉，下次有事再开一个新的。

支持飞书 / Lark、Discord、Telegram、Slack、钉钉，以及 GitHub 的 issue 和 PR。

```
 私聊 / 群里 @bot ─┐
 GitHub 动态 ──────┴─▶ claude-bot gateway ──Jev 筛选──▶ 项目的 .claude-bot/inbox/
                              │                                 │
                              └─ 有待办：在仓库里启动 ─▶ Claude Code (/bot)
                                                       │  claude-bot inbox    取出这一批
                                                       │  claude-bot reply    在原消息下回复
                                                       └─ claude-bot finish   这一批做完，gateway 关掉它
```

claude-bot 本身只负责**收发消息、启停 Claude**。理解需求、决定怎么做、怎么回复，都由 Claude 按项目里的 `.claude/commands/bot.md` 自己判断。详见[工作原理](docs/how-it-works.md)。

## 特性

- **不需要公网地址**：所有渠道都是从本机主动连出去（长连接或轮询），不用配域名、HTTPS 和回调地址。
- **一个 gateway 管所有项目**：本机一个 `claude-bot gateway` 进程服务所有登记过的项目。每个项目有自己的 bot，可以同时接多个渠道；配置存在本地 SQLite（`~/.claude-bot/gateway.db`），消息进各项目自己的 `.claude-bot/inbox/`，互不影响。
- **不用开着终端**：在 gateway 的网页上给项目打开「自动处理」，有待办时 gateway 自己启动 Claude Code。一个 Claude 只做一批，`finish` 之后连同它启动的所有进程一起关掉；空闲时不花 token。每个项目最多一个 Claude，所有项目一共默认最多 3 个。
- **本地网页看全局**：哪个渠道断了、为什么、怎么修；每个项目在处理哪些事、整段对话和 bot 的回复；在页面上添加和修改渠道、测试连接；Claude 的终端也在页面上，能看、能打字、能中断。桌面 App 就是这个网页的独立窗口。
- **Jev 先筛一遍**：每条消息先经过 [TypeSafe](https://typesafe.ai) 的 Jev 模型，归成一件件事，追问（24 小时内）并回原来那件事，附件提前下载好；非法请求直接回复「现在暂时不支持。」，不交给 Claude。见 [PROTOCOL.md §1.1](PROTOCOL.md#11-jev-筛选)。
- **整批处理**：Claude 一次取出所有待办，能直接答的先回，要改代码的几件合进一个 PR、一次发版，每件事分别回复。
- **改代码一律走 PR**：Claude 自己开的 PR 验证后合并、发版；别人的 PR 先判断值不值得要，再在本地实际跑过才合并。
- **消息不会丢**：消息一到就写进本地收件箱并加表情回应；Claude 中断、电脑重启，没做完的事下次接着做。
- **几分钟接入**：`claude-bot init` 选渠道。飞书在浏览器里点一下「确认」就建好应用；其他渠道粘贴 token 即可。
- **工具保持简单**：单个 Go 二进制，网页打包在里面，没有别的服务要装。值守流程是一个 Markdown 文件，可以按项目约定修改。

## 环境要求

- macOS 或 Linux
- [Claude Code](https://claude.com/claude-code)，已登录
- git，以及 [GitHub CLI](https://cli.github.com)（`gh`，已登录；Claude 用它开 PR）
- [TypeSafe](https://console.typesafe.ai/keys) 的 API key（Jev 筛选用，必需）
- 飞书 / Lark、Discord、Telegram、Slack、钉钉或 GitHub 中任意一个的账号

## 安装

**Apple Silicon 的 Mac（M 系列芯片）**：从 [Releases](https://github.com/hewenyu/claude-bot/releases) 下载 `claude-bot-<版本>-arm64.dmg`，把 claude-bot 拖进「应用程序」。打开 App 就会启动 gateway 并显示它的网页。

App 没有签 Apple 的开发者证书、也没有公证（开源项目），第一次打开会被 macOS 拦下。在终端里运行一次：

```bash
xattr -dr com.apple.quarantine /Applications/claude-bot.app
```

或者先双击打开一次，再到「系统设置 → 隐私与安全性」里点「仍要打开」。App 里带着 `claude-bot` 命令，把它链接到 PATH 上，终端里用的就和 App 是同一个版本：

```bash
sudo ln -sf /Applications/claude-bot.app/Contents/Resources/claude-bot /usr/local/bin/claude-bot
```

**只要命令行**：[Releases](https://github.com/hewenyu/claude-bot/releases) 里有 `darwin-arm64`、`linux-amd64`、`linux-arm64` 的二进制（附 `SHA256SUMS`）。比如 Linux amd64：

```bash
curl -fsSL https://github.com/hewenyu/claude-bot/releases/latest/download/claude-bot-<版本>-linux-amd64.tar.gz | tar -xz claude-bot
sudo install claude-bot /usr/local/bin/
```

`<版本>` 是 Releases 上的 tag，比如 `v0.3.2`。在 Mac 上用浏览器下载的话，先 `xattr -d com.apple.quarantine claude-bot`；用 `curl` 下载不需要。

**用 Go 安装**（Go 1.27+）：

```bash
go install github.com/hewenyu/claude-bot/cmd/claude-bot@latest
```

或者从源码构建：`git clone` 之后 `make build`，生成 `bin/claude-bot`。

## 快速开始

1. **登记项目**：在要值守的仓库里运行

   ```bash
   claude-bot init
   ```

   第一次会要 TypeSafe API key（所有项目共用，只填一次），然后选渠道、建好 bot、把项目登记进 gateway，并生成 `.claude/commands/bot.md`（值守流程，提交进仓库）。各渠道怎么建应用、拿 token 见[接入各个渠道](docs/channels.md)。

2. **启动 gateway**：用桌面 App 的话打开它就行；否则在自己的终端里运行

   ```bash
   claude-bot gateway start   # 在后台启动，一个进程服务所有项目（stop 停止，restart 重启）
   claude-bot gateway open    # 在浏览器里打开 gateway 的网页
   ```

   要在自己的终端里启动，gateway 和它启动的 Claude 才用得上你的 PATH 和代理等环境变量。

3. **打开「自动处理」**：在网页上进这个项目的「自动处理」标签，打开开关。之后有待办，gateway 就在仓库里启动 Claude Code 跑 `/bot`。

   不想让 gateway 来跑，也可以自己在仓库里打开 Claude Code，运行 `/bot`，它处理完这一批就停。两种方式别同时用在一个项目上，不然两个 Claude 会抢同一批任务。

4. **把 bot 拉进项目群**，之后大家私聊它或在群里 @ 它就可以了。

再值守一个项目，在那个仓库里 `claude-bot init` 就行，gateway 几秒内自动接上，不用重启。

> **安全**：「自动处理」启动的 Claude 以你的身份运行，默认跳过权限确认（`bypassPermissions`），能读写你能读写的一切。只给信任的项目打开，项目最好放在给它专用的机器上；`bot.md` 就是给它的指令，改它要像改代码一样审。见[自动处理](docs/how-it-works.md#自动处理)。

升级之后要做什么、以前用 `claude-bot start` 单独跑监听进程的项目怎么迁进 gateway，见[常见问题](docs/faq.md)。

## 文档

- [接入各个渠道](docs/channels.md)：飞书 / Lark、Discord、Telegram、Slack、钉钉、GitHub 的接入步骤、权限和限制
- [工作原理](docs/how-it-works.md)：消息怎么收进来、一批事怎么处理、「自动处理」怎么启停 Claude
- [命令与配置](docs/reference.md)：全部命令和 `init` 参数、gateway 的网页、文件、环境变量、代理
- [常见问题](docs/faq.md)：连不上、升级、从单项目监听进程迁到 gateway
- [开发](docs/development.md)：构建、测试和代码结构
- [PROTOCOL.md](PROTOCOL.md)：消息协议、CLI 约定，以及怎么接入新渠道

## 社区

本项目在 [LINUX DO](https://linux.do) 社区分享和推广，感谢佬友们的支持与反馈。有问题、有想法，欢迎在 LINUX DO 的帖子里或者 GitHub issue 里提。

[![LINUX DO](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LINUX%20DO-1f7aec?style=for-the-badge)](https://linux.do)

## 许可证

[MIT](LICENSE)。`.claude/skills/` 下的 skill 来自 [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang)，沿用其各自声明的 MIT 许可。
