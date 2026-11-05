---
name: claude-bot gateway
description: The local page onto the gateway — which channels are up, what the bot is working on, and the full record of each task.
colors:
  kiln-terracotta: "#c8643f"
  kiln-terracotta-deep: "#b4532e"
  terracotta-wash: "#fbeee7"
  fired-clay-ink: "#9c4524"
  on-accent: "#ffffff"
  running-green: "#2f9e5b"
  running-green-ink: "#1f7a43"
  running-green-wash: "#e7f5ec"
  amber-wait: "#c98a12"
  amber-wait-ink: "#8a5a00"
  amber-wait-wash: "#fcf3dd"
  fault-red: "#d14343"
  fault-red-ink: "#b02a2a"
  fault-red-deep: "#b83232"
  fault-red-wash: "#fcebea"
  note-blue: "#3b6fd8"
  note-blue-ink: "#2a56b8"
  note-blue-wash: "#e9f0fc"
  idle-stone: "#a39f96"
  idle-stone-wash: "#f0eee9"
  logbook-paper: "#f6f5f2"
  clean-page: "#ffffff"
  margin-paper: "#fbfaf8"
  logbook-ink: "#1d1c1a"
  pencil-grey: "#6f6b63"
  ruled-line: "#e7e3dc"
  ruled-line-strong: "#d6d1c7"
  bench-charcoal: "#1f1e1c"
  bench-charcoal-raised: "#2a2926"
  chalk: "#e9e6df"
  worn-chalk: "#9b978d"
  bot-reply-wash: "#fff4ee"
  bot-reply-line: "#f2d8c9"
typography:
  headline:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "22px"
    fontWeight: 600
    letterSpacing: "-0.01em"
  title:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "-0.01em"
  subtitle:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "15px"
    fontWeight: 600
    letterSpacing: "-0.01em"
  body:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
  body-sm:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.55
  caption:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "11px"
    fontWeight: 600
    letterSpacing: "0.08em"
  mono:
    fontFamily: "ui-monospace, \"SF Mono\", \"JetBrains Mono\", Menlo, Consolas, monospace"
    fontSize: "0.92em"
rounded:
  xs: "4px"
  sm: "6px"
  control: "7px"
  md: "8px"
  lg: "10px"
  bubble: "12px"
  xl: "14px"
  full: "999px"
spacing:
  xxs: "4px"
  xs: "6px"
  sm: "8px"
  md: "10px"
  lg: "14px"
  xl: "16px"
  2xl: "22px"
  3xl: "28px"
components:
  button:
    backgroundColor: "{colors.clean-page}"
    textColor: "{colors.logbook-ink}"
    typography: "{typography.body-sm}"
    rounded: "{rounded.control}"
    padding: "5px 12px"
  button-primary:
    backgroundColor: "{colors.kiln-terracotta-deep}"
    textColor: "{colors.on-accent}"
    typography: "{typography.body-sm}"
    rounded: "{rounded.control}"
    padding: "5px 12px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.pencil-grey}"
    rounded: "{rounded.control}"
    padding: "5px 12px"
  button-ghost-hover:
    backgroundColor: "{colors.idle-stone-wash}"
    textColor: "{colors.logbook-ink}"
  button-danger:
    backgroundColor: "{colors.fault-red-deep}"
    textColor: "{colors.on-accent}"
    rounded: "{rounded.control}"
    padding: "5px 12px"
  input:
    backgroundColor: "{colors.clean-page}"
    textColor: "{colors.logbook-ink}"
    rounded: "{rounded.control}"
    padding: "6px 10px"
  card:
    backgroundColor: "{colors.clean-page}"
    rounded: "{rounded.lg}"
    padding: "14px 16px"
  nav-item:
    textColor: "{colors.chalk}"
    rounded: "{rounded.md}"
    padding: "8px 10px"
  nav-item-active:
    backgroundColor: "{colors.bench-charcoal-raised}"
    textColor: "{colors.chalk}"
  tab:
    textColor: "{colors.pencil-grey}"
    padding: "8px 12px"
  tab-active:
    textColor: "{colors.logbook-ink}"
  pill:
    backgroundColor: "{colors.idle-stone-wash}"
    textColor: "{colors.pencil-grey}"
    rounded: "{rounded.full}"
    padding: "0 7px"
    height: "18px"
  tag-ok:
    backgroundColor: "{colors.running-green-wash}"
    textColor: "{colors.running-green-ink}"
    padding: "1px 7px"
  tag-warn:
    backgroundColor: "{colors.amber-wait-wash}"
    textColor: "{colors.amber-wait-ink}"
    padding: "1px 7px"
  bubble-user:
    backgroundColor: "{colors.margin-paper}"
    textColor: "{colors.logbook-ink}"
    rounded: "{rounded.bubble}"
    padding: "9px 13px"
  bubble-bot:
    backgroundColor: "{colors.bot-reply-wash}"
    textColor: "{colors.logbook-ink}"
    rounded: "{rounded.bubble}"
    padding: "9px 13px"
  banner-warn:
    backgroundColor: "{colors.amber-wait-wash}"
    textColor: "{colors.logbook-ink}"
    rounded: "{rounded.lg}"
    padding: "10px 14px"
  banner-bad:
    backgroundColor: "{colors.fault-red-wash}"
    textColor: "{colors.logbook-ink}"
    rounded: "{rounded.lg}"
    padding: "10px 14px"
  command:
    backgroundColor: "{colors.margin-paper}"
    rounded: "{rounded.md}"
    padding: "6px 6px 6px 12px"
  modal:
    backgroundColor: "{colors.clean-page}"
    rounded: "{rounded.xl}"
    width: "520px"
  toast:
    backgroundColor: "{colors.bench-charcoal}"
    textColor: "{colors.chalk}"
    rounded: "9px"
    padding: "9px 14px"
---

# Design System: claude-bot gateway

## Overview

**Creative North Star: "The Workshop Logbook"**

The gateway page is the logbook kept next to a workbench. Warm, slightly creamy paper holds the record: which project is connected, which task the bot picked up, every message in the thread and every reply it wrote. A dark charcoal bench runs down the left edge, where the projects sit like tools on a rail. One terracotta stamp, Kiln Terracotta, marks what is yours to act on, and it marks almost nothing else. Everything else on the page is ink, pencil and ruled lines.

The page is for checking in, not for spending time in. It is calm at rest, so the moment something breaks it reads instantly, because the only saturated colors on the paper are status colors. The feel is soft and friendly: rounded corners, generous padding and conversation bubbles that read like a chat you were part of. Don't let the look drift toward a terminal or an operations console. Density is moderate. Rows stay on one line, and Chinese text never breaks after every character.

The world rules out three things by name: the SaaS dashboard look (KPI tiles, charts, gradient hero cards), the cold developer-tool look (blue-grey neon dark consoles) and decoration of any kind (illustrations, mascots, ornamental gradients). Type is the system font stack, so the page stays self-contained inside the Go binary.

**Key Characteristics:**
- Warm paper and ink neutrals, with a dark charcoal sidebar ("the bench").
- One brand accent (Kiln Terracotta), used for the primary action, active navigation and the bot's voice.
- A fixed status vocabulary: green running, amber waiting or starting, red fault, stone idle, blue note.
- Surfaces separated by hairline borders and tonal steps, not by shadow.
- Soft, friendly forms: 7–10px radius on controls and panels, 12–14px on bubbles and dialogs, full rounding on pills.
- System fonts only, CJK-first stack, with a monospace face for paths, ids and commands.
- Light and dark themes follow `prefers-color-scheme`. Both are first-class.

## Colors

Warm paper-and-ink neutrals, one fired-clay accent, and a small set of status colors that are the only other saturated hues allowed on the page.

### Primary
- **Kiln Terracotta** (#c8643f; dark theme #e07a52): The brand stamp. Used for the active-tab underline, the 2px inset bar on the active sidebar item, the focus ring, hover borders on choosable options, the accent pill and the brand mark.
- **Kiln Terracotta Deep** (#b4532e, both themes): The accent as a fill under white text: the primary button and the count pill. White on it reads 4.98:1, where white on Kiln Terracotta is only 3.9:1.
- **Terracotta Wash** (#fbeee7; dark #3a2519): The selected-row fill in the task list, the checked state of option checkboxes and the tile behind empty-state icons.
- **Fired Clay Ink** (#9c4524; dark #f1a07f): Terracotta as text. Used for links, the bot's name in a conversation and the glyph in empty-state icons. Use this, not Kiln Terracotta, whenever the accent has to be read as text.

### Status
These carry state and nothing else. Each has three forms: the hue (dots, chip and divider rules, banner borders), an ink for words in that status (state text, tag text, inline errors, test results; 4.5:1 or better on every surface and wash), and a wash (tag, banner and test-result backgrounds). Dark-theme inks: green #5cc98a, amber #e8b04a, red #f07a7a, blue #7fa6f0.
- **Running Green** (#2f9e5b), its ink and its wash (#e7f5ec; dark #173223): connected, running, test passed.
- **Amber Wait** (#c98a12), its ink and its wash (#fcf3dd; dark #3a2e12): starting, busy, needs attention but not broken, and the "new messages" divider.
- **Fault Red** (#d14343), its ink and its wash (#fcebea; dark #3d1c1b): failed, disconnected (including a running project with one channel stopped), errors, destructive actions and required-field marks. **Fault Red Deep** (#b83232) is the fill under white text: the final destructive confirm and failure toasts.
- **Note Blue** (#3b6fd8), its ink and its wash (#e9f0fc; dark #1a2a46): informational notes and banners.
- **Idle Stone** (#a39f96) and **Idle Stone Wash** (#f0eee9; dark #2a2825): stopped or unknown, and neutral pills, skeletons and the segmented control's track.

### Neutral
- **Logbook Paper** (#f6f5f2; dark #171614): The page background behind everything in the main column.
- **Clean Page** (#ffffff; dark #1f1e1b): Panels, cards, inputs, dialogs and sticky headers. One tonal step up from the paper.
- **Margin Paper** (#fbfaf8; dark #23221f): The step between paper and page. Used for user message bubbles, command blocks and row hover.
- **Logbook Ink** (#1d1c1a; dark #ece9e2): Primary text.
- **Pencil Grey** (#6f6b63; dark #a19c92): Secondary text, metadata, inactive tabs and ghost buttons.
- **Ruled Line** (#e7e3dc; dark #2f2d29): Hairline borders and dividers on every surface.
- **Ruled Line Strong** (#d6d1c7; dark #3d3a35): Button and input borders, the dashed outline of empty panels and the small separator rule in meta rows.
- **Bench Charcoal** (#1f1e1c; dark #121110): The sidebar and toast background.
- **Bench Charcoal Raised** (#2a2926; dark #1c1b19): Sidebar hover and active fills, sidebar pills and the footer rule.
- **Chalk** (#e9e6df) and **Worn Chalk** (#9b978d): Text and secondary text on the bench. The same in both themes.
- **Bot Reply Wash** (#fff4ee; dark #2b211b) and **Bot Reply Line** (#f2d8c9; dark #4a3326): The bot's message bubble, a terracotta-tinted paper so its voice is recognizable without reading the name.

### Named Rules
**The One Stamp Rule.** Kiln Terracotta marks the single thing to act on (one filled primary button per view), the current location (active tab, active sidebar item) or the bot's voice. It is never used as a background tint for a whole region, and never as a status color.

**The Status-Only Saturation Rule.** Apart from the accent, the only saturated hues on the page are the five status colors, and they appear only where they report real state. A green dot means connected, and nothing is green for decoration.

**The Ink Rule.** Words never take a status hue or Kiln Terracotta directly at 11–13px: they take the matching ink (`--ok-text`, `--warn-text`, `--bad-text`, `--info-text`), and white text sits only on the deep fills. The hues stay for dots, rules and borders.

**The Two Themes Rule.** Every color is defined for both themes in `:root`, and the dark set is overridden under `prefers-color-scheme: dark`. A new color isn't done until it has a dark value.

## Typography

**Display Font:** none. The page has no display tier.
**Body Font:** system-ui (with -apple-system, PingFang SC, Hiragino Sans GB, Microsoft YaHei, sans-serif)
**Label/Mono Font:** ui-monospace (with SF Mono, JetBrains Mono, Menlo, Consolas, monospace)

**Character:** The operating system's own voice, Latin and CJK alike, with no web fonts to load. The monospace face is there to mark machine text (paths, ids, file names, commands), not to set a mood.

### Hierarchy
- **Headline** (600, 22px, letter-spacing -0.01em): The project name at the top of a project view. There is one per page.
- **Title** (600, 16px, 1.4): Conversation titles, dialog titles and empty-state titles.
- **Subtitle** (600, 15px): Channel card titles.
- **Body** (400, 14px, 1.55): The default for everything, including message bubbles.
- **Body Small** (400–500, 13px): Buttons, form labels (weight 500), card fields, project meta and banners' secondary text.
- **Caption** (400–500, 12px): Row metadata, bubble headers, timestamps, the sidebar's sub-lines and status text (weight 500).
- **Label** (600, 11px, letter-spacing 0.08em, uppercase): Sidebar section labels. Tags and pills share the 11px size at weights 600 and 500.
- **Mono** (0.92em of its context): Inline code, paths (12px), file names and command blocks.

### Named Rules
**The No-Wrap Meta Rule.** Metadata in a row stays on one line. Only the free-text part (senders, titles) is allowed to truncate with an ellipsis. Everything else keeps its width, because CJK text would otherwise break after every character.

**The Machine Text Rule.** Anything a user might copy into a terminal (paths, ids, commands, token hints) is set in mono. Prose never is.

## Layout

The page is an app shell: a fixed 264px sidebar (the bench) and a fluid main column with padding of 22px top, 28px sides and 40px bottom. The main column is a vertical stack with a 14px gap. A project view stacks its head (name, state, path, channel health chips), a tab row and the tab's content.

The task view is a two-pane split: a task list of 300–380px beside the conversation, which takes the rest. Each pane scrolls on its own and has a sticky header. Channel cards sit in an auto-fill grid (minimum 360px per card, 14px gap) and stretch to equal height, so their footers line up. Within a card, fields are a two-column definition grid whose label column is at least 170px, so values start at the same x across cards. Settings and explanatory text are capped at 760px.

Spacing runs on a small set of steps (4, 6, 8, 10, 14, 16, 22, 28px). 14px is the default gap between panels, and 6–10px the gap inside rows. The conversation timeline uses 18px padding and 14px between messages.

Below 960px the shell stacks into a single `minmax(0, 1fr)` column, so a long path or command can never widen the page. The sidebar folds into two rows: the brand with 共用设置 beside it, then the projects as one sideways-scrolling strip (paths hidden, states shown), the active one underlined in Kiln Terracotta. The main padding drops to 16px, the task view becomes one column and card fields stack label-over-value.

When a project has no task to open, the task list takes the full width on its own, and its empty state says why.

## Elevation & Depth

The system is flat by direction. Surfaces are told apart by tonal steps (Logbook Paper → Margin Paper → Clean Page) and by Ruled Line hairlines, not by shadow. Overlays are the only things that float. A dialog lifts on a large soft shadow over a warm, 45%-opaque dark backdrop, and a toast is a charcoal slip in the bottom-right corner.

Cards, the task list and the conversation pane carry no shadow, only their hairline border. A bordered list inside a card (the GitHub tokens) is ruled off with top and bottom hairlines, never boxed a second time.

### Shadow Vocabulary
- **Overlay lift** (`box-shadow: 0 20px 60px rgb(0 0 0 / 0.3)`): Dialogs only.
- **Toast lift** (`box-shadow: 0 1px 2px rgb(30 25 15 / 0.05), 0 4px 16px rgb(30 25 15 / 0.05)`; dark `0 1px 2px rgb(0 0 0 / 0.3), 0 4px 16px rgb(0 0 0 / 0.25)`): Toasts, which float over content.
- **Selected segment** (`box-shadow: 0 1px 2px rgb(0 0 0 / 0.08)`): The "on" button in a segmented control. This is a state cue, not elevation.

### Named Rules
**The Flat Page Rule.** Nothing that sits in the page flow casts a shadow. A shadow means "floating above the page" (dialog, toast) or "currently selected" (segment), and never "this is a card".

## Shapes

The forms are soft and friendly. Corners are rounded everywhere: 4px on inline code, skeleton bars and the corner of a bubble's tail; 6px on channel badges and segment buttons; 7px on buttons, inputs, file rows and inline results; 8px on sidebar items, command blocks and option tiles; 10px on panels, cards and banners; 12px on message bubbles; 14px on dialogs and empty-state icon tiles. Pills, chips and avatars are fully round, and status dots are 8px circles (6px inside tags).

Message bubbles have one tightened corner (4px) on the speaker's side: top-left for people, top-right for the bot. That is the tail, without drawing a tail.

Borders are 1px hairlines. Dashed borders mean "empty or addable": empty panels and the add-a-channel card. When softness has to be adjusted, err rounder and roomier, never tighter.

## Components

### Buttons
Quiet outlines by default, with exactly one terracotta fill where the main action is.
- **Shape:** gently rounded (7px), with 5px × 12px padding and 13px text. An optional 6px gap holds an icon.
- **Default:** Clean Page fill, Ruled Line Strong border, Logbook Ink text. On hover the border darkens to Pencil Grey.
- **Primary:** Kiln Terracotta Deep fill and border, white text. On hover it brightens 8% (`filter: brightness(1.08)`). A save button turns primary only once its field has something to save, so a page of forms never shows two at rest.
- **Ghost:** transparent and borderless with Pencil Grey text. On hover it gets an Idle Stone Wash fill and Logbook Ink text. Used for copy, close and secondary row actions.
- **Danger:** a Fault Red Deep fill for the final confirm of a destructive action. **Danger text** (a ghost button in Fault Red ink, Fault Red Wash on hover) is for a destructive action that sits among other controls, such as 移除项目 in the project header, so it doesn't shout until it's meant.
- **Disabled:** 55% opacity and a default cursor.
- **Sizes:** extra-small (12px, 1px × 7px) for in-row actions. The icon button (18px icon) is for close.

### Status indicators
- **Dot:** an 8px circle in a status color. A live connection pulses (1.6s ease-in-out to 35% opacity), and the pulse stops under `prefers-reduced-motion`.
- **Tag:** an 11px, weight-600 label on a status wash with the status color as text, radius 5px and an optional 6px dot. Used for task state.
- **State text:** a 12px, weight-500 dot plus label in the status color. Used for project and channel state.
- **Chip:** a fully round, hairline-bordered capsule holding a channel badge and its name. It takes a red-tinted border when that channel is failing.

### Pills
- **Style:** fully round, 18px tall, 11px weight-500 text, Idle Stone Wash with Pencil Grey text. Used for counts.
- **Accent pill:** a Kiln Terracotta Deep fill with white text, for counts of open tasks ("2 进行中"). In the sidebar, neutral pills take the Bench Charcoal Raised fill.

### Cards / Containers
- **Corner style:** 10px.
- **Background:** Clean Page on Logbook Paper.
- **Shadow strategy:** none (see The Flat Page Rule).
- **Border:** a 1px Ruled Line. A dashed Ruled Line Strong border means an empty panel or the add-a-channel card.
- **Internal padding:** 14px × 16px with a 10px internal gap. The footer is pinned to the bottom behind a hairline rule.
- **Inline error:** Fault Red text on Fault Red Wash, 7px radius, 6px × 10px, wrapping anywhere so long ids and paths don't overflow.

### Inputs / Fields
- **Style:** Clean Page fill, 1px Ruled Line Strong border, 7px radius, 6px × 10px padding, inheriting the 14px body text.
- **Focus:** the global 2px Kiln Terracotta ring with a 2px offset.
- **Labels:** 13px weight 500 above the field, with a red asterisk for required fields. Validation messages are 12px Fault Red text beneath.
- **Checkbox tiles:** a bordered 7px tile. When checked, its border turns Kiln Terracotta and its fill Terracotta Wash.
- **Credentials:** a stored secret is never shown. The field shows only a hint of its last characters and a placeholder inviting a new value.

### Navigation
- **Sidebar:** Bench Charcoal with Chalk text. The brand block (mark, name, live status line) sits on top, then an uppercase 11px section label with a count, then the project items, broken ones first, then waiting ones, then the rest. Settings is pinned to the bottom behind a hairline rule. A healthy project's sub-line is its path; any other project's is its state in words ("钉钉 已停止", "出错"). In the dark theme a hairline separates the bench from the page.
- **Sidebar item:** 8px × 10px with an 8px radius, a status dot, the name (weight 500) and a 12px Worn Chalk sub-line. On hover it gets the Bench Charcoal Raised fill. When active it gets the same fill plus a 2px Kiln Terracotta inset bar on the left edge.
- **Tabs:** an underline tab row on a hairline. Inactive tabs are Pencil Grey weight 500. The active tab is Logbook Ink with a 2px Kiln Terracotta underline.
- **Segmented control:** a group of `aria-pressed` buttons on an Idle Stone Wash track with a 2px inset and 6px-radius segments. The selected segment becomes Clean Page with the selected-segment shadow. Counts sit inside in Pencil Grey, with tabular numbers.

### Conversation (signature component)
A task's full thread, read like a chat you were part of.
- **Layout:** people on the left with a 28px round initial avatar. The bot is on the right with no avatar.
- **People's bubbles:** Margin Paper with a Ruled Line border.
- **Bot's bubbles:** Bot Reply Wash with a Bot Reply Line border, and the bot's name in Fired Clay Ink.
- **Bubble size and text:** up to 680px or 85% of the pane, padded 9px × 13px, and wrapping anywhere.
- **Header:** a 12px line with the sender (weight 600) and time.
- **Attachments:** hairline rows with a mono file name.
- **New messages:** an Amber Wait divider with a hairline on each side, labelled in 12px weight 600.
- **Markdown:** inside bubbles, inline code gets a 7% ink tint and code blocks a Clean Page box.

### Command block (signature component)
A terminal command to run, with its copy button. It is a Margin Paper strip with a Ruled Line border and 8px radius. The command is mono, on one line, and scrolls horizontally. A ghost "复制" button sits on the right and reads "已复制" for 1.5s after use. It appears in empty states, banners and dialogs, wherever the next step happens in a terminal.

### Banners and toasts
- **Banner:** a full-width strip at the top of the main column, 10px radius, padded 10px × 14px. It uses a status wash with a 30–35% status-tinted border. "Bad" banners use `role="alert"`; the others use `role="status"`. Each one says what happened and what to do next.
- **Toast:** a Bench Charcoal slip in the bottom-right corner, 9px radius, 13px text, at most 380px wide. It disappears after 4s. A failure toast is Fault Red with white text.

### Icons
Drawn, not typed: a small set of 24-unit line icons (`web/src/icons.tsx`) at a 1.75 stroke with round caps and joins, in the text color. They cover the inbox, check, cross, chat, power, folder, key, gear and reply. An icon is sized by the font-size around it; empty states set it at 26px on a 52px Terracotta Wash tile. Unicode glyphs never stand in for icons.

### Channel badge
A channel's official logo, drawn as is with no tile behind it (GitHub's logo takes the text color). It is 28px in cards and 18px inline. A channel without a logo gets a lettered grey tile (#6b7280, white text), which picks up a faint inner ring in dark mode.

## Do's and Don'ts

### Do:
- **Do** keep Kiln Terracotta (#c8643f) for one primary action per view, the current location and the bot's voice (The One Stamp Rule).
- **Do** use a status color only to report real state, with its wash as the background and the strong form as text or dot.
- **Do** separate surfaces with a 1px Ruled Line and a tonal step from Logbook Paper to Clean Page.
- **Do** define every new color for both themes under `prefers-color-scheme`.
- **Do** set paths, ids, file names and commands in the mono stack, and give terminal commands the copyable command block.
- **Do** keep row metadata on one line and let only free text truncate (The No-Wrap Meta Rule).
- **Do** wrap error text anywhere (`overflow-wrap: anywhere`), because it carries ids and paths that don't break on their own.
- **Do** keep forms soft: 7px on controls, 10px on panels, 12–14px on bubbles and dialogs. When unsure, round up and add room.
- **Do** set status words in the status ink, never in the bare hue (The Ink Rule).
- **Do** give anything a dot or a tooltip says a screen-reader equivalent in the text (`SrOnly`), inside a positioned container so it can't widen a scrolling strip.
- **Do** honor `prefers-reduced-motion`. The status pulse and skeleton shimmer are the only ambient motion, and both stop.

### Don't:
- **Don't** build a SaaS dashboard: no KPI tiles, charts, big-number stat cards or gradient hero cards.
- **Don't** drift into a cold developer-tool look: no blue-grey neon dark consoles. The dark theme stays warm charcoal and chalk.
- **Don't** add decoration: no illustrations, mascots, ornamental gradients or background patterns.
- **Don't** load web fonts. The page ships inside the Go binary and uses the system stack.
- **Don't** put shadows on in-page surfaces such as cards, lists and panes (The Flat Page Rule).
- **Don't** use Kiln Terracotta as a status color or as a fill for whole regions.
- **Don't** display a stored credential. Show its last few characters at most.
- **Don't** use Unicode glyphs or emoji as icons; draw them in `icons.tsx`.
