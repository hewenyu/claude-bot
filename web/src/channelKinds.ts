// What the page knows about each kind of channel, beside the form the
// gateway sends for it (channelTypes in cmd/claude-bot/gateway_web_channels.go):
// its short name, where its credentials live, its part of the setup guide.

export type ChannelKind = "lark" | "dingtalk" | "telegram" | "discord" | "slack" | "github";

interface ChannelFacts {
  /** Its name in a sentence; the gateway's own label can be longer ("飞书 / Lark"). */
  label: string;
  /** Where its credentials and settings live. */
  console: { label: string; href: string };
  /** Its section of docs/channels.md. */
  docAnchor: string;
  /** Its threads are public issues and PRs, not group chats or DMs. */
  issues?: boolean;
}

const DOCS = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md";

export const channelKinds = {
  lark: { label: "飞书", console: { label: "飞书开放平台", href: "https://open.feishu.cn/app" }, docAnchor: "#飞书--lark" },
  dingtalk: { label: "钉钉", console: { label: "钉钉开发者后台", href: "https://open-dev.dingtalk.com" }, docAnchor: "#钉钉" },
  telegram: { label: "Telegram", console: { label: "BotFather", href: "https://t.me/BotFather" }, docAnchor: "#telegram" },
  discord: { label: "Discord", console: { label: "Discord 开发者后台", href: "https://discord.com/developers/applications" }, docAnchor: "#discord" },
  slack: { label: "Slack", console: { label: "Slack 应用后台", href: "https://api.slack.com/apps" }, docAnchor: "#slack" },
  github: { label: "GitHub", console: { label: "GitHub token 设置", href: "https://github.com/settings/tokens" }, docAnchor: "#github", issues: true },
} satisfies Record<ChannelKind, ChannelFacts>;

export function isChannelKind(name: string): name is ChannelKind {
  return Object.hasOwn(channelKinds, name);
}

/** What the page knows about a channel; undefined for one newer than the page. */
export function channelFacts(name: string): ChannelFacts | undefined {
  return isChannelKind(name) ? channelKinds[name] : undefined;
}

/** The channel's name in a sentence; an unknown channel goes by its own name. */
export function channelLabel(name: string): string {
  return channelFacts(name)?.label ?? name;
}

/** The setup guide for a channel, at its section when it has one. */
export function channelDocs(name: string): string {
  return DOCS + (channelFacts(name)?.docAnchor ?? "");
}
