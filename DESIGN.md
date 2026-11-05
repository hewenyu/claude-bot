---
name: claude-bot gateway
description: The local page onto the gateway — what needs you, what the bot is doing, and the full record of each task.
colors:
  periwinkle: "#5b5bd6"
  periwinkle-ink: "#4646c0"
  periwinkle-wash: "#efeffd"
  orb-glow: "#e0e0ff"
  orb-mid: "#8f8ff2"
  live-green: "#30a46c"
  live-green-ink: "#18794e"
  live-green-wash: "#e9f6ef"
  waiting-amber: "#f0a020"
  waiting-amber-ink: "#9a4d00"
  waiting-amber-wash: "#fff4e0"
  broken-red: "#e5484d"
  broken-red-ink: "#c52a2f"
  broken-red-deep: "#c52a2f"
  broken-red-wash: "#feeeef"
  off-grey: "#b3b3bd"
  page-white: "#ffffff"
  surface: "#ffffff"
  rail-grey: "#f7f7f8"
  sunken: "#f4f4f6"
  hover: "#eeeef2"
  active: "#e8e8ee"
  hairline: "#ececf1"
  hairline-strong: "#d9d9e1"
  ink: "#0d0d0d"
  ink-secondary: "#63636e"
  badge-grey: "#6b7280"
typography:
  headline:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "28px"
    fontWeight: 650
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  headline-sm:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "26px"
    fontWeight: 650
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  title:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "20px"
    fontWeight: 650
    lineHeight: 1.35
    letterSpacing: "-0.01em"
  title-sm:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.4
  body:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
  record:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "14.5px"
    fontWeight: 400
    lineHeight: 1.7
  control:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "13.5px"
    fontWeight: 500
  meta:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "12.5px"
    fontWeight: 400
  caption:
    fontFamily: "system-ui, -apple-system, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", sans-serif"
    fontSize: "12px"
    fontWeight: 500
  mono:
    fontFamily: "ui-monospace, \"SF Mono\", \"JetBrains Mono\", Menlo, Consolas, monospace"
    fontSize: "0.92em"
rounded:
  xs: "6px"
  sm: "8px"
  control: "10px"
  md: "12px"
  lg: "14px"
  card: "18px"
  overlay: "22px"
  full: "999px"
spacing:
  2xs: "4px"
  xs: "6px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  2xl: "32px"
  3xl: "40px"
components:
  button:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.full}"
    padding: "0 14px"
    height: "34px"
  button-hover:
    backgroundColor: "{colors.hover}"
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.page-white}"
    typography: "{typography.control}"
    rounded: "{rounded.full}"
    padding: "0 14px"
    height: "34px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.full}"
    padding: "0 14px"
    height: "34px"
  button-danger:
    backgroundColor: "{colors.broken-red-deep}"
    textColor: "#ffffff"
    rounded: "{rounded.full}"
    padding: "0 14px"
    height: "34px"
  button-sm:
    typography: "{typography.meta}"
    rounded: "{rounded.full}"
    padding: "0 10px"
    height: "28px"
  icon-button:
    backgroundColor: "transparent"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.control}"
    size: "32px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "38px"
  rail-item:
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "7px 10px"
  rail-item-active:
    backgroundColor: "{colors.active}"
  tab:
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.full}"
    padding: "0 16px"
    height: "36px"
  tab-active:
    backgroundColor: "{colors.active}"
    textColor: "{colors.ink}"
  chip:
    textColor: "{colors.ink}"
    rounded: "{rounded.full}"
    padding: "0 10px 0 7px"
    height: "28px"
  count:
    backgroundColor: "{colors.periwinkle-wash}"
    textColor: "{colors.periwinkle-ink}"
    rounded: "{rounded.full}"
    padding: "0 6px"
    height: "20px"
  count-bad:
    backgroundColor: "{colors.broken-red-wash}"
    textColor: "{colors.broken-red-ink}"
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
    padding: "18px 20px"
  problem-card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
    padding: "16px 18px"
  rows-panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
    padding: "6px"
  row:
    rounded: "{rounded.md}"
    padding: "10px 12px"
  command:
    backgroundColor: "{colors.sunken}"
    rounded: "{rounded.md}"
    padding: "4px 4px 4px 14px"
  banner-bad:
    backgroundColor: "{colors.broken-red-wash}"
    textColor: "{colors.broken-red-ink}"
    rounded: "{rounded.lg}"
    padding: "12px 16px"
  banner-warn:
    backgroundColor: "{colors.waiting-amber-wash}"
    textColor: "{colors.waiting-amber-ink}"
    rounded: "{rounded.lg}"
    padding: "12px 16px"
  banner-info:
    backgroundColor: "{colors.periwinkle-wash}"
    textColor: "{colors.periwinkle-ink}"
    rounded: "{rounded.lg}"
    padding: "12px 16px"
  dock:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.full}"
    padding: "0 14px 0 20px"
    height: "52px"
  modal:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.overlay}"
    width: "540px"
  palette:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.overlay}"
    width: "640px"
  toast:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.page-white}"
    rounded: "{rounded.full}"
    padding: "10px 18px"
---

# Design System: claude-bot gateway

## Overview

**Creative North Star: "The Briefing"**

The gateway page reads like the home screen of a current AI product, not an operations console. A quiet grey rail lists the projects the way a history list would, and one centered column answers the only two questions the visitor has: what needs me, and what is the bot doing. The headline counts the things that need you. Each of those things is a card in plain language that carries its own fix. Below sit the tasks in progress, and the healthy projects collapse to a single line of chips.

The page is white and almost colourless. Ink-black does the work of emphasis: the primary action is an ink pill, the numbered fix steps are ink discs. One periwinkle accent belongs to the gateway's presence: a soft matte orb that breathes while a bot works and takes a calm red tint when something is broken. The same periwinkle marks selection, focus and links, and nothing else. Status colours appear only where state is reported, and state is always carried by shape as well as colour.

A task's message record is a read-only log, not a conversation. Entries are separated by hairlines, the bot's entries are tagged with a small periwinkle label, and nobody speaks in bubbles. Type is the operating system's own, because the page ships inside a Go binary.

**Key Characteristics:**
- White page, soft grey rail, ink-black primary pills, one periwinkle accent.
- A presence orb as the page's only signature ornament.
- State marks that read without colour: filled, ring, struck, hollow, mini-orb.
- Problem cards with a plain diagnosis, numbered fix steps, a command to copy and the raw error.
- Generous rounding: 18px panels, 22px overlays, pills for every button, tab and chip.
- Flat in the page; shadows only on things that float, plus an open problem card.
- System fonts and tabular figures; light and dark themes follow `prefers-color-scheme`.

## Colors

A near-neutral white-and-ink palette, one periwinkle accent, and Radix-like status hues that each come with a separate ink and wash.

### Primary
- **Periwinkle** (`periwinkle`; dark #8484f0): The focus ring (2px, 2px offset), the input focus border and its 22% halo, text selection (28% mix), the caret, the checked option tile's border, the hover border of a choosable add-a-channel tile, and the deepest stop of the orb.
- **Periwinkle Ink** (`periwinkle-ink`; dark #a9a9fa): The accent as text: links, step links, the "working" state word, the bot's role tag, counts and the info banner.
- **Periwinkle Wash** (`periwinkle-wash`; dark #23234a): Behind counts, the bot's role tag, info banners, the empty-state icon tile, the "where to get it" strip in forms and a checked option tile.
- **Orb Glow** and **Orb Mid** (`orb-glow`, `orb-mid`; dark #d4d4ff, #9393f5): The two lighter stops of the orb's radial gradient (centre at 50% 45%), and of the mini-orb state mark. They are not used anywhere else.

### Status
Each state has a hue for marks and tinted borders, an ink for words (4.5:1 or better on white and on its wash) and a wash for backgrounds.
- **Live Green** (`live-green`, ink, wash; dark #3dbf7f / #5fd49a / #142b1f): Connected, running, test passed, a copy just done.
- **Waiting Amber** (`waiting-amber`, ink, wash; dark #f0a836 / #ffbe5c / #33260f): Starting, busy, waiting for the bot, port taken, and the "not yet handled" divider in a task record.
- **Broken Red** (`broken-red`, ink, wash; dark #ec5d5e / #ff8a8a / #3a1a1c): Failed, disconnected, errors, required-field marks, destructive menu items. **Broken Red Deep** (same in both themes) is the fill under white text: the destructive confirm and a failure toast.
- **Off Grey** (`off-grey`; dark #5c5c66): The hollow ring of a stopped or unknown project.

### Neutral
- **Page White** (`page-white`; dark #0f0f10): The page.
- **Surface** (`surface`; dark #1a1a1a): Cards, rows panels, inputs, the dock, menus, dialogs, the palette. Identical to the page in light; one step up in dark.
- **Rail Grey** (`rail-grey`; dark #171717): The project rail and the task list panel.
- **Sunken** (`sunken`; dark #1f1f1f): Command strips, raw errors, notes, code in the record, disabled inputs, row hover.
- **Hover** and **Active** (`hover`, `active`; dark #262626, #2e2e2e): Hover fills on rail items, tabs, chips and menus; the active rail item, tab and task row; the segmented control's track.
- **Hairline** and **Hairline Strong** (`hairline`, `hairline-strong`; dark #2a2a2a, #3b3b3b): Every border and divider; button, input and kbd borders and the dashed empty panel.
- **Ink** (`ink`; dark #ededed): Text, and the fill of the primary pill, step numbers and toasts.
- **Ink Secondary** (`ink-secondary`; dark #a3a3a3): Metadata, inactive tabs, ghost buttons, placeholders, labels.
- **Badge Grey** (`badge-grey`): The lettered tile for a channel without an official logo.

### Named Rules
**The One Periwinkle Rule.** Periwinkle belongs to presence, selection, focus and links. It never fills a button, never tints a region larger than a strip or tile, and never stands in for a status.

**The Ink Pill Rule.** Emphasis is ink, not colour. The action to take is an ink-black pill (`button-primary`), one per card, form or dialog; a save button turns primary only once its field holds something to save.

**The Ink-for-Words Rule.** Words in a state take that state's ink; the bare hue is for marks and tinted borders only. White text sits only on Broken Red Deep and on the ink pill in light mode.

**The Two Themes Rule.** Every token is defined in `:root` and overridden under `prefers-color-scheme: dark`. In dark, tinted card borders fall back to the plain hairline so red and amber don't glow.

## Typography

**Display Font:** none; the page has no display tier.
**Body Font:** system-ui (with -apple-system, PingFang SC, Hiragino Sans GB, Microsoft YaHei, sans-serif)
**Label/Mono Font:** ui-monospace (with SF Mono, JetBrains Mono, Menlo, Consolas, monospace)

**Character:** The operating system's own voice for Latin and CJK alike, set tight at the top (650 weight, negative tracking) and calm everywhere else. Mono marks machine text; it never sets a mood.

### Hierarchy
- **Headline** (650, 28px, 1.25, -0.02em; 23px below 880px): The home briefing's sentence, beside the orb. Balanced wrapping.
- **Headline Small** (650, 26px, 1.25, -0.02em): A project's name. The offline and sign-in screens use 24px at the same weight.
- **Title** (650, 20px, 1.35, -0.01em): A task's title above its record. Dialog titles use 17px at 650.
- **Title Small** (600, 15px, 1.4): Section headings, problem titles, step titles in the big welcome steps. Card and empty-state titles use 16px at 600.
- **Body** (400, 14px, 1.55): Everything by default, including rail items and inputs.
- **Record** (400, 14.5px, 1.7, max 720px): The body of an entry in a task record.
- **Control** (500, 13.5px): Buttons, step detail, links under steps. Meta lines and chips use 13px.
- **Meta** (400, 12.5px): Row sub-lines, task meta, raw-error header, small buttons.
- **Caption** (500, 12px): Rail sub-lines and group labels, palette group labels, counts (600), timestamps. Pass labels and kbd use 11.5px.
- **Mono** (0.92em of context): Commands, paths, raw errors, ids, the version number.

Weights run 400 / 500 / 550 / 600 / 650; 550 is the "selected" weight on active rail items, tabs and form labels.

### Named Rules
**The Two-Size List Rule.** A list row has two sizes: a title and a sub-line. Rank inside it by weight, not by adding a third size.

**The Machine Text Rule.** Anything the user might paste into a terminal (commands, paths, ids, raw errors) is mono; prose never is.

**The Tabular Figures Rule.** Times, counts and the reply tally use `tabular-nums` so columns don't jitter as they update.

## Layout

The shell is a 264px rail beside a fluid main column. The rail is sticky and scrolls on its own: brand (orb + name), a jump field with its ⌘K hint, 总览, the project list (broken first, then waiting, then the rest) and, pinned at the foot behind a hairline, shared settings and the gateway's uptime and version.

Pages are centered columns padded 44px top, 40px sides, 72px bottom. Home and settings use the narrow column (max 840px); a project uses the wide one (max 1320px). On home the briefing header is followed by sections 40px apart (28px for the first), stacked cards 12px apart and a floating dock pinned to the bottom of the column, which content fades under (a gradient from transparent to the page at 45%).

A project stacks its header (name, state, path, channel chips), pill tabs and the tab's content. Tasks split into a 340px sticky list on a rail-grey panel and the record (max 820px), 28px apart; 300px and 20px below 1080px. Channel cards sit in an auto-fill grid of at least 340px with 16px gaps; fields inside are a label/value grid with a label column of at least 130px.

Spacing steps are 4, 6, 8, 12, 16, 24, 32, 40px, with 2px between list items and 10px/14px inside controls.

Below 880px the rail becomes a drawer (min(300px, 86vw), a dimmed backdrop) opened from a 52px sticky top bar that also carries search, so the dock is hidden. Padding drops to 24px 16px 56px, problem actions wrap under the text, rows drop their time and logos, the task view becomes one column, the pass header becomes a two-column grid and fields stack.

## Elevation & Depth

Flat in the page, lifted when floating. Depth inside the page comes from the rail's grey, the sunken grey of command strips, and hairline borders. Shadows mark what floats above the page: the dock, menus, dialogs, the palette, toasts and the mobile drawer. Two state responses borrow the float shadow: an open problem card (the one being read) and a choosable add-a-channel tile on hover (which also lifts 1px). Overlays dim the page with `rgb(12 12 20 / 0.38)`.

### Shadow Vocabulary
- **Float** (`0 10px 30px -8px rgb(20 20 40 / 0.14), 0 2px 6px rgb(20 20 40 / 0.06)`; dark `0 10px 30px -8px rgb(0 0 0 / 0.5), 0 2px 6px rgb(0 0 0 / 0.3)`): The dock at rest, menus, toasts, an open problem card, a hovered add tile.
- **Pop** (`0 24px 60px -16px rgb(20 20 40 / 0.28), 0 4px 12px rgb(20 20 40 / 0.08)`; dark `0 24px 60px -16px rgb(0 0 0 / 0.7), 0 4px 12px rgb(0 0 0 / 0.35)`): Dialogs, the palette, the open drawer, the dock on hover.
- **Segment on** (`0 1px 3px rgb(20 20 40 / 0.1)`): The selected button in a segmented control; a state cue, not elevation.

### Named Rules
**The Float-Only Shadow Rule.** Nothing in the page flow casts a shadow at rest. A shadow means floating (dock, menu, dialog, palette, toast, drawer) or a state the user caused (the open problem card, a hovered choice, a selected segment).

## Shapes

Soft and round. Every button, tab, chip, count, toast, the dock and the bot's role tag is a full pill (999px). Panels are 18px (cards, problem cards, rows panels, the task list, empty panels); dialogs and the palette are 22px; banners, menus, the pass header, add tiles and the empty-state icon tile are 14px; rows, task rows, command strips, raw errors, problem icon tiles, palette items and notes are 12px; inputs, rail items, the jump field and icon buttons are 10px; segment buttons and large badges 8px; kbd, badges and inline code 6px.

Marks and the orb are perfect circles. Borders are 1px hairlines; a dashed border means empty or addable (empty task panel, the add-a-channel card).

### Named Rules
**The Shape-and-Colour Rule.** A state mark is a 10px circle (8px in chips, rail status and healthy chips) whose shape carries the state on its own: filled is up, a 2.5px ring is waiting, filled with a struck bar is broken, a 2px hollow grey ring is off, a mini-orb is the bot at work. Colour confirms; it never carries state alone.

## Components

### Buttons
Pills throughout; ink speaks, outlines wait.
- **Shape:** full pill, 34px tall, 14px sides, 13.5px weight 500, 6px icon gap (15px icons).
- **Default:** surface fill, strong hairline border, ink text; hover fill.
- **Primary:** ink fill and border, page-coloured text; hover mixes 18% of the page into the ink. Used for the fix on a problem card and a form's submit.
- **Ghost:** transparent, secondary ink; hover turns the text ink. "怎么修 / 收起" and copy buttons.
- **Danger:** Broken Red Deep fill with white text for the final destructive confirm. **Danger text** is a ghost in Broken Red ink with a red wash on hover.
- **Small:** 28px, 10px sides, 12.5px. **Icon button:** 32px, 10px radius, 17px icon, hover fill; a copy icon turns Live Green ink when done.
- **Disabled:** 50% opacity, no pointer events.

### State marks, counts and chips
- **Mark:** see The Shape-and-Colour Rule. Working marks breathe (2s, scale 0.72).
- **State text:** mark plus a 13px weight-500 word in the state's ink.
- **Count:** 20px pill, 12px weight 600, periwinkle wash and ink; red wash for broken counts, hover grey for quiet ones.
- **Chip:** 28px hairline pill with a channel logo or mark and a name; a broken channel's chip takes a 40% red-tinted border.

### Cards / Containers
- **Corner style:** 18px.
- **Background:** surface on the page; the task list sits on rail grey without a border.
- **Shadow strategy:** none at rest (The Float-Only Shadow Rule).
- **Border:** 1px hairline; broken cards mix 28-32% Broken Red into it, waiting ones 40% Waiting Amber (plain hairline in dark).
- **Internal padding:** 18px × 20px with a 16px gap; a footer is pinned to the bottom behind a hairline.
- **Rows panel:** a 6px-padded panel of 12px-radius rows (10px × 12px) that hover to sunken.

### Inputs / Fields
- **Style:** 38px, 10px radius, strong hairline, surface fill, 14px text, secondary-ink placeholder.
- **Focus:** periwinkle border plus a 3px periwinkle halo at 22%.
- **Labels:** 13px weight 550 above, a red asterisk when required.
- **Option tiles:** a 12px hairline tile with a checkbox; checked turns the border periwinkle and the fill periwinkle wash.
- **Disabled:** sunken fill, secondary ink.

### Navigation
- **Rail:** rail-grey, 12px × 10px padding, 2px between items (a hairline on its right edge in dark only). Items are 36px min, 10px radius, 14px; hover fill, active fill plus weight 550. A project item holds a mark, its name, an optional state sub-line in the state's ink and an open-task count.
- **Group label:** 12px weight 500 secondary ink, sentence case.
- **Tabs:** 36px pills, 16px sides, 14px weight 550 secondary ink; active gets the active fill and ink, with a count inside.
- **Segmented control:** a 3px-padded hover-grey track (11px radius) of 30px buttons; the "on" one is surface with the segment shadow.
- **Mobile:** a 52px sticky top bar (menu, orb + name, search) on the page colour with a bottom hairline; the rail slides in as a drawer.

### Presence orb (signature component)
A matte radial disc of light, 18 / 24 / 44px (36px on phones beside the headline). Calm is periwinkle; **working** breathes (2.8s, scale 1.08, opacity 0.82); **broken** turns a soft red (#fde3e3 → #f3a9ab → #e3676b); **off** turns grey (#ececf0 → #c9c9d1 → #9d9da8). It appears beside the brand (calm or working only), beside the home headline (where it turns red when anything is broken) and on the offline and sign-in screens (off); the gateway has one presence.

### Problem card (signature component)
What happened, the fix, and how. A 40px tinted icon tile (12px radius, the state's wash and ink, or the channel's logo), a 15px title, a one-line plain-language diagnosis, a ghost "怎么修" toggle and one ink-pill action (open the right console, copy a command, or go to the editor). Opened, it gains the float shadow and unfolds (0.28s) behind a hairline into numbered steps: 24px ink discs, a 550-weight step title, secondary detail, an optional command strip or link. Under the steps, the platform's raw error sits in a sunken mono block with its own copy button. The first problem is open on arrival.

### Command strip
A sunken 12px strip, mono 13px on one line that scrolls sideways, with a copy icon button that confirms with a check for 1.5s. Used wherever the next step happens in a terminal.

### Task record (signature component)
A read-only log. The header is a title, actions and a **pass**: one 14px-radius hairline strip of labelled segments (11.5px label over a 13.5px weight-550 value) for source, state, participants, bot replies and created time. Entries run down a single column separated by hairlines, 18px apart: a 13px head (sender in 600 or the periwinkle "bot" role pill, then the time) over the body at 14.5/1.7. Attachments are file lines with a paperclip. Messages the bot hasn't handled sit under an amber divider with a hairline on each side.

### Dock and jump palette (signature component)
The dock is a 52px surface pill with the float shadow, a search icon, a prompt and a ⌘K kbd; on hover it rises 1px to the pop shadow. ⌘K (or the dock, the rail field, the mobile search) opens the palette: 640px, 22px radius, pop shadow, dropped 14vh from the top. A 58px input row over grouped results (12px group labels), each item a 12px-radius row with a logo, mark or icon, a title, a secondary sub-line and an arrow on the highlighted item.

### Banners, dialogs and toasts
- **Banner:** 14px radius, 12px × 16px, the state's wash and ink with a 16px icon; bad banners are `role="alert"`.
- **Dialog:** 540px (660px wide), 22px radius, pop shadow, rises in (0.26s); hairline above the footer.
- **Toast:** an ink pill at bottom centre, 13.5px, float shadow, gone after 4s; a failure toast is Broken Red Deep.

### Icons and channel badges
Icons are drawn line icons in `icons.tsx`, one stroke weight, in the text colour, sized by the surrounding font size. Channel badges are official logos drawn as is (GitHub's takes the ink); a channel without one gets a lettered Badge Grey tile.

**The app icon** (`docs/logo.svg`, inlined as the page's favicon, and above the README title) is an ink tile with 15/64 rounding, a white terminal prompt `›` and the presence orb after it: the bot, awake in the terminal. The orb uses the orb's own gradient and stops. The SVG follows `prefers-color-scheme`, lifting the tile and its edge in dark so it doesn't sink into a dark tab bar. Inside the page the orb alone stays the brand mark, because it also reports presence.

### Motion
One easing, `cubic-bezier(0.16, 1, 0.3, 1)`, for unfolding, popping, rising and the drawer (0.16–0.32s). Ambient motion is limited to the orb, working marks and skeleton fades. `prefers-reduced-motion` turns every animation and transition off.

## Do's and Don'ts

### Do:
- **Do** make the action an ink-black pill, one per card, form or dialog (The Ink Pill Rule).
- **Do** keep periwinkle (#5b5bd6) for presence, selection, focus and links (The One Periwinkle Rule).
- **Do** carry every state with shape and colour together: filled, ring, struck, hollow, mini-orb (The Shape-and-Colour Rule).
- **Do** set state words in the state's ink, with its wash behind banners, tiles and counts.
- **Do** write a problem as a plain diagnosis, numbered steps, a command to copy and the raw error.
- **Do** keep in-page surfaces flat and give the float or pop shadow only to things that float (The Float-Only Shadow Rule).
- **Do** round generously: pills for controls, 18px for panels, 22px for overlays.
- **Do** set commands, paths, ids and raw errors in mono, and times and counts in tabular figures.
- **Do** define every new colour for both themes.
- **Do** honor `prefers-reduced-motion`.

### Don't:
- **Don't** frame the task record as a chat: no bubbles, no avatars, no assistant voice. It is a read-only log.
- **Don't** fill a button or a region with periwinkle, or use it as a status colour.
- **Don't** report state with colour alone.
- **Don't** load web fonts; the page ships inside the Go binary and uses the system stack.
- **Don't** put a shadow on a card, row panel or list at rest.
- **Don't** use Unicode glyphs or emoji as icons; draw them in `icons.tsx`.
- **Don't** display a stored credential beyond its last few characters.
