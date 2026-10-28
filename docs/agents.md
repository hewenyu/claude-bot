# 用其他 agent 值守

claude-bot 的消息层（`start`、`inbox`、`get`、`download`、`reply`、`done`）是普通的命令行接口，和 agent 无关。值守流程只有一份：`init` 生成的 `.claude/commands/bot.md`。同时 `init` 会建一个符号链接 `.agents/skills/bot/SKILL.md` 指向它，读 Agent Skills 的 agent 就能直接用。改流程时只改 `bot.md`。

| Agent | 状态 | 怎么跑 |
|---|---|---|
| [Claude Code](https://claude.com/claude-code) | 支持 | `/loop /bot` |
| [pi](https://github.com/earendil-works/pi) | 支持 | 装上 claude-bot 自带的 `/loop` 扩展后运行 `/loop /skill:bot` |

不管用哪个 agent，监听进程都要你自己在终端里用 `claude-bot start` 启动，见[工作原理](how-it-works.md)。

## pi

pi 本身没有循环命令，claude-bot 带了一个 `/loop` 扩展。

**1. 安装扩展**

在项目仓库里运行：

```bash
claude-bot init --pi
```

它会写入 `.pi/extensions/claude-bot-loop.ts`。如果还没有值守流程，也会一起生成 `.claude/commands/bot.md` 和 `.agents/skills/bot/SKILL.md`。这条命令不动渠道配置；还没接渠道的话，先运行一次 `claude-bot init`。升级 claude-bot 之后，再运行一次 `claude-bot init --pi` 就会更新扩展。

`.pi/extensions` 是项目级扩展，pi 只在项目被信任之后才加载。第一次在这个仓库里打开 pi 时它会询问，选信任即可；也可以加 `-a` 只信任这一次。

**2. 开始值守**

在仓库里打开 pi，运行：

```
/loop /skill:bot
```

`/loop` 的用法：

| 命令 | 作用 |
|---|---|
| `/loop [间隔] <prompt>` | 马上发送 prompt；之后每次 agent 跑完、停下来，等上这段间隔再发一次。间隔写成 `30s`、`5m`、`1h`，默认 `1m` |
| `/loop stop` | 停止循环。按 Esc 中断正在跑的一轮，也会停止循环 |
| `/loop` | 查看当前在循环什么 |

每一轮里，值守流程会运行 `claude-bot inbox --wait 9m`，最多等 9 分钟消息，所以实际节奏由收件箱决定，间隔只是两轮之间的停顿。在两轮之间自己输入消息不会打乱循环：等你这一轮跑完，循环会接着来。

**注意**

- pi 的 bash 工具默认没有超时，`inbox --wait 9m` 不会被中途打断。
- 用你平时启动 pi 的方式启动它。比如模型服务在本机（`127.0.0.1`）的话，不要让 `HTTPS_PROXY` 之类的代理变量把它也代理掉。

不想用项目级扩展的话，也可以把 `claude-bot-loop.ts` 复制到 `~/.pi/agent/extensions/`，这样所有项目都能用；但升级 claude-bot 之后就要自己再复制一次。
