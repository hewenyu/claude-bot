# 接入各个渠道

在项目仓库里运行 `claude-bot init`，它会先让你选渠道（飞书 / Lark、Discord、Telegram、Slack、GitHub、钉钉）。至少接一个，其他的之后再运行 `claude-bot init --channel <渠道>` 补上，多个渠道可以同时用。选 Discord、Telegram、Slack、GitHub 或钉钉时，它会提示你输入 token（输入时不显示），获取方法见下面各渠道的说明。不在终端里运行时（比如脚本，或者 Claude Code 的 Bash 工具），不会弹出菜单，默认接飞书；要接其他渠道，用 `--channel` 或对应的 token 参数指定。

不管选哪个渠道，`init` 都会把这个项目登记进 gateway、把凭证存进 `~/.claude-bot/gateway.db`（权限 `0600`），把 `.claude-bot/` 加进 `.gitignore`，生成值守流程 `.claude/commands/bot.md`（已经存在就保留），最后等你私聊 bot 一条消息，收到「连接成功」的回复就说明收发都已打通（GitHub 没有私聊，只检查 token 能不能访问仓库）。`init` 的全部参数见[命令与配置](reference.md#命令)。

- [飞书 / Lark](#飞书--lark)
- [Discord](#discord)
- [Telegram](#telegram)
- [Slack](#slack)
- [钉钉](#钉钉)
- [GitHub](#github)

## 飞书 / Lark

`init` 会打开浏览器，走飞书官方的应用注册授权流程（终端里同时显示二维码，也可以用飞书 App 扫码）。登录后点「确认」，就会创建好应用，并配置好机器人能力、[权限](#权限)和事件订阅（长连接），不用去开放平台手动配置。已经有应用的话，用 `--app-id` 和 `--app-secret` 接入。

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

## Discord

1. 在 [Discord Developer Portal](https://discord.com/developers/applications) 新建一个应用，在 Bot 页面点 Reset Token 拿到 token。
2. 在 `init` 的菜单里选 Discord 后粘贴 token，或者直接运行：

   ```bash
   claude-bot init --discord-token <token>
   ```

   它会验证 token，把它存进 gateway，打印把 bot 邀请进服务器的链接（已带上所需权限），然后等你私聊 bot 来验证连接。

不需要开启 Message Content 这个特权 intent：私聊和 @ 了 bot 的消息，Discord 总会给出正文，而 bot 只处理这两种。在服务器里要 @ bot 本人；@ 它的同名角色不会被投递。

权限：邀请链接会申请这几项：查看频道、发消息、在子区（thread）里发消息、读取消息历史、添加表情回应。

## Telegram

1. 在 Telegram 里找 [@BotFather](https://t.me/BotFather)，发 `/newbot` 建一个 bot，拿到 token。每个项目建一个。
2. 建议关掉隐私模式：给 @BotFather 发 `/setprivacy`，选这个 bot，再选 `Disable`。开着隐私模式时，群里 @bot 的普通消息可能收不到。关掉后 bot 也只处理 @ 它或回复它的消息。如果 bot 已经在群里，要移出群再拉回来才生效。
3. 在 `init` 的菜单里选 Telegram 后粘贴 token，或者直接运行：

   ```bash
   claude-bot init --telegram-token <token>
   ```

   它会验证 token，存进 gateway，然后等你私聊 bot 来验证连接。之后把 bot 拉进群即可，话题（topic）群也支持。

Telegram 的 Bot API 没法按 ID 读取一条消息，所以 `get` 在 Telegram 上不可用。作为替代，回复某条消息时，被回复的内容会以引用（`> 名字: …`）的形式放在 `text` 开头。国内访问 Telegram 需要代理，默认使用环境变量里的代理，见[代理](reference.md#代理)。

权限：不需要额外申请。bot 收到消息后会用 👀 表情回应，群里关掉了表情回应的话这一步会静默失败，不影响收发。

## Slack

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

   它会验证两个 token，存进 gateway，然后等你私信 bot 来验证连接。之后在频道里 `/invite @bot`。

在频道里 @bot 的消息，回复会发在这条消息的 thread 里，并 @ 提问的人；私信里直接回复。回复里的 `<!channel>`、`<!here>` 这类全员提醒不会生效。

权限：manifest 里的 scope 依次用于：收 @bot 的消息、发消息、收私信、用 `get` 读取频道 / 私有频道 / 群聊私信里的消息、下载附件、用 👀 表情回应。

## 钉钉

钉钉用企业内部应用的机器人，消息通过 Stream 模式（WebSocket 长连接）推过来，同样不需要公网地址和回调 URL。

1. 在[钉钉开发者后台](https://open-dev.dingtalk.com)创建一个企业内部应用（需要是企业的管理员或子管理员）。
2. 在应用里添加「机器人」能力，消息接收模式选 **Stream 模式**，然后发布机器人。
3. 在「权限管理」里开通「企业内机器人发送消息权限」。下载附件等用到的其他权限如果没开，调用时钉钉会在报错里写明缺哪一个，按提示开通即可。
4. 在「版本管理与发布」里发布应用，让成员能搜到这个机器人。
5. 在「凭证与基础信息」里找到 Client ID（原 AppKey）和 Client Secret（原 AppSecret）。在 `init` 的菜单里选钉钉后依次粘贴，或者直接运行：

   ```bash
   claude-bot init --dingtalk-client-id <Client ID> --dingtalk-client-secret <Client Secret>
   ```

   它会验证凭证，存进 gateway，然后等你私聊机器人来验证连接。之后在群设置 → 机器人里把它加进群。

钉钉和其他渠道相比有几处不同，都是开放接口本身的限制：

- 群里的回复会以「@昵称」开头，但钉钉的接口发不了真正的 @，对方不会收到 @ 提醒。
- 没有给机器人用的表情回应接口，收到消息时不会回 👀。
- 读不了历史消息，所以 `get` 不可用。
- `send` 只能发到群（`dingtalk:<conversationId>`）。私聊只能通过回复对方的消息来发。
- 私聊回复要用对方的企业员工 ID。组织外的人私聊机器人时，消息能收到，但回复不了。

## GitHub

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

   仓库默认从 GitHub 上的 remote 读出来（先看 `origin`）。仓库还没有 GitHub 上的 remote（比如只在本地的仓库）时没有要监听的东西，菜单里不能选 GitHub；要监听别的仓库就用 `--github-repo <owner>/<name>` 指定。它会检查 token 能不能访问这个仓库、有没有写权限。

token 存在 gateway 里，所有项目共用一个默认 token，第二个项目接 GitHub 时不用再给。有项目属于另一个账号（比如公司组织的仓库和你自己的仓库），给它单独一个有名字的 token：

```bash
claude-bot init --github-token <另一个账号的 token> --github-token-name work
```

之后别的项目要用这个 token 就 `claude-bot init --github-token-name work`。也可以在 gateway 的网页里管理：「共用设置」里添加 token、勾选它服务哪些项目，或者在项目的 GitHub 渠道里选用哪个 token。只有一个 token 时不用管这些，所有项目都用默认的。

用谁的 token，回复就显示成谁发的。可以单独注册一个机器人账号，也可以直接用自己的。用自己的也没关系：bot 发的评论和开的 PR 末尾都带一个隐藏标记 `<!-- claude-bot -->`，只有带标记的才会被跳过，你自己手写的评论和 PR 照样会进收件箱，也会被当成别人的 PR 来审。

监听进程第一次启动时，会把还开着、而且最后一条评论不是 bot 回复的 issue 和 PR 放进收件箱；已经处理过（回复过或者 `done` 过）的不会再进来。

回复之外的操作用 `claude-bot github`：

```bash
claude-bot github close <id> [--not-planned]               # 关 issue 或 PR
claude-bot github merge <id> [--method squash|merge|rebase] # 合并 PR，默认 squash
claude-bot github review <id> approve|request-changes|comment [text|-]
```

`<id>` 是收件箱里的 `github:<owner>/<repo>/<编号>`，带 `#c…`（评论）、`#r…`（行内 review 评论）、`#v…`（review）后缀的也可以，会作用在所属的 issue 或 PR 上。
