# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Solo developers running a bot for their own repositories, on their own macOS or Linux machine. They are fluent with git, `gh` and Claude Code, and live in a terminal. The person who opens the gateway page is the person who ran `claude-bot init`; nobody else sees it. They open it to check on things, not to sit in it: between other work, or when a chat message got no reply.

## Product Purpose

claude-bot connects chat (Feishu / Lark, Discord, Telegram, Slack, DingTalk) and GitHub issues and PRs to a Claude Code session that stays resident in the repository (`/loop /bot`). Someone messages or @-mentions the bot. Claude Code investigates, changes the code, runs the tests, opens a PR and replies under the original message.

claude-bot itself only carries messages: it puts incoming messages in each project's local inbox and provides a reply command. Claude decides what a message means and what to do about it. Success: a message sent from the phone turns into a verified PR and a reply, without the developer opening a laptop.

The gateway page (`claude-bot gateway open`) is the local window onto that process. Its jobs, in order:

1. **Spot what's broken:** which project or channel is disconnected or erroring, and why.
2. **Follow the work:** what each project is handling right now, with each task's whole conversation and the bot's replies.
3. **Manage projects:** which projects are registered, where they live on disk, removing one.

Editing channel credentials and the TypeSafe key also happens on the page, but `claude-bot init` does most of that setup in the CLI. Treat it as a supporting job.

## Positioning

The bot is deliberately minimal: channels only, with Claude doing all the judging. The parts claude-bot owns are:

- **No public address.** Every channel connects outbound over a long connection or long polling, so there's no domain, HTTPS or webhook to set up.
- **One local gateway process** serves every registered project. Config lives in one local SQLite file (`~/.claude-bot/gateway.db`). Messages still go to each project's own `.claude-bot/inbox/`.
- **Jev screening is mandatory.** Every message first goes through TypeSafe's Jev model. It groups messages into tasks, attaches follow-ups (within 24 hours) to the original task, downloads attachments, and refuses illegal requests before Claude sees them.
- **Code changes always go through a PR** and are merged only after they've actually been verified.
- **Messages are never lost.** They're written to disk and acknowledged with a reaction on arrival, survive restarts, and are handled one at a time in order.

## Operating Context

- Install with `go install`. One Go binary embeds the web page (built from `web/` and committed to `internal/webui/static`), so installing needs no Node.
- `claude-bot init` registers a project. `claude-bot gateway start|stop|restart|open` runs the gateway. Claude Code runs `/bot` in each repo and processes the inbox through `claude-bot inbox --wait` and `claude-bot reply`. Either the gateway runs it (a project's 自动处理 is on), or a person keeps `/loop /bot` open in a terminal. Never both in one project.
- The page opens only on the local machine, with an access token in the URL fragment (`#token=…`). It polls the gateway every few seconds. It has to handle the gateway going offline, the TypeSafe key missing, and projects that have been removed.
- Development uses a demo gateway with fake data (every project state, no real platforms): `CLAUDE_BOT_PAGE_DEMO=127.0.0.1:7601 go test -run TestPageDemo -timeout 0 ./cmd/claude-bot`, then `http://127.0.0.1:7601/#token=de30de30`.

## Capabilities and Constraints

- **Gateway page stack:** React 19, TypeScript and Vite, with no UI library or other runtime dependencies. The one exception is `@xterm/xterm` (with its fit addon) for the agent terminal: a terminal emulator that renders Claude Code correctly is not something to hand-roll. It loads lazily with the first terminal, so the rest of the page stays light. Run `make web` to rebuild into `internal/webui/static`, and commit the result. `make web-check` enforces that the committed build matches `web/`.
- **Runner (自动处理):** the gateway starts Claude Code with `/bot` in a project whose runner is on, when its inbox has tasks. There is one agent per project, and at most `runner_max_parallel` across all projects (default 3). An idle agent yields its place and resumes its session on news. The runner decides when; `/bot` (the project's `bot.md`) decides what. The runner has no task list of its own. Its agents run in bypass-permissions mode as the user, so they can read `~/.claude-bot`; the page and docs say so.
- **Desktop app:** `desktop/` is an Electron window around the page, not a second UI. It starts the gateway in a login shell and opens the tokenized URL. Anything the app shows belongs in the page.
- **Project states:** running, starting, busy, failed, stopped. Run states: queued, running, blocked (等你确认), waiting (需要你看看), idle (值守中), resting, failed, off. Channel states: connected, connecting, not connected, error, stopped.
- **Credentials only go in.** The page never shows a stored secret, only its last few characters. After an edit, the gateway reconnects within seconds.
- **Handled tasks archive themselves** and come back to the list when someone follows up.
- **Terms in use:** 项目 (project), 渠道 (channel), 事 / task, 收件箱 (inbox), 筛选 (screening by Jev), 归档 (archive), 自动处理 (the runner), agent / Claude, 名额 (a place among the parallel agents), gateway, TypeSafe API key.
- **Supported agent:** Claude Code is the only supported agent. Other agents would get separate adapters, not features in this product.
- **Scope:** keep the bot minimal. New surface area needs a reason the user has asked for.

## Brand Commitments

- The name is always lowercase: `claude-bot`, and the page calls itself `claude-bot gateway`.
- **UI language:** Chinese for now. Write copy so it can be translated to English later, rather than hard-wiring sentence fragments together.
- **Existing voice** (from the README and UI copy): plain, direct, concrete Chinese in short declarative sentences that say what happens and what to do next. For example, 「和 gateway 断开了，正在重连…下面是断开前的数据。」 No marketing tone.
- **Channel logos:** channels are shown with their platforms' official logos (`web/src/logos.tsx`).

## Evidence on Hand

- README tagline: 「在群里 @ 一下，Claude Code 就去仓库里把活干了。」
- Docs: `README.md`, `docs/` (channels, how-it-works, reference, faq, development) and `PROTOCOL.md`.
- The project is shared in the LINUX DO community.
- **None of these exist:** user counts, testimonials, benchmarks or case studies. Don't invent them.

## Product Principles

1. **Claude judges; claude-bot carries.** UI and tooling expose state and move messages. They don't replicate Claude's decisions.
2. **Show the truth of the process.** Connection state, errors and task history are shown as they are. When something is broken, say what broke and what to do about it.
3. **Built for checking in.** The page answers "is everything fine, and what is the bot doing?" within seconds. It isn't a place to sit in.
4. **Local and self-contained.** One binary, one SQLite file, one local page. No hosted services, public endpoints or extra installs.
5. **Secrets never come back out.** A credential that has been entered can be replaced or tested, but never displayed.
