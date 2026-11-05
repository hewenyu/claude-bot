// What needs the user, in plain words, with the steps that fix it. The
// gateway reports raw errors from each platform; this turns the ones we
// recognize into a diagnosis and keeps the raw text alongside.

import { channel, shortPath } from "./format";
import { href } from "./route";
import type { Overview, ProjectStatus } from "./types";

export interface Step {
  title: string;
  detail?: string;
  command?: string;
  link?: { label: string; href: string };
}

export interface Problem {
  /** Stable across polls, for open/closed state. */
  key: string;
  tone: "bad" | "warn" | "info";
  project?: ProjectStatus;
  /** The channel it is about, when there is one. */
  channel?: string;
  title: string;
  /** The title inside the project's own page, without its name. */
  local?: string;
  summary: string;
  steps: Step[];
  /** The main thing to do: a page to open or a command to copy. */
  action?: { label: string; href: string; external?: boolean } | { label: string; command: string };
  raw?: string;
}

const DOCS = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md";

/** Where each platform's credentials and settings live. */
export const consoles: Record<string, { label: string; href: string }> = {
  lark: { label: "飞书开放平台", href: "https://open.feishu.cn/app" },
  dingtalk: { label: "钉钉开发者后台", href: "https://open-dev.dingtalk.com" },
  slack: { label: "Slack 应用后台", href: "https://api.slack.com/apps" },
  discord: { label: "Discord 开发者后台", href: "https://discord.com/developers/applications" },
  telegram: { label: "BotFather", href: "https://t.me/BotFather" },
  github: { label: "GitHub token 设置", href: "https://github.com/settings/tokens" },
};

const docAnchor: Record<string, string> = {
  lark: "#飞书--lark",
  dingtalk: "#钉钉",
  slack: "#slack",
  discord: "#discord",
  telegram: "#telegram",
  github: "#github",
};

/** The setup guide for a channel. */
export function channelDocs(name: string): string {
  return DOCS + (docAnchor[name] ?? "");
}

const RESTART: Step = {
  title: "重启 gateway，让它重新连接",
  detail: "平台那边的设置改了，要重连一次才生效。",
  command: "claude-bot gateway restart",
};

interface Cause {
  test: RegExp;
  title: (label: string) => string;
  steps: (name: string, label: string, channels: string) => Step[];
  action?: (name: string, channels: string) => Problem["action"];
}

const platformLink = (name: string) => (consoles[name] ? { label: tidy(`打开${sp(consoles[name].label)}`), href: consoles[name].href } : undefined);

// Order matters: the first match wins.
const causes: Cause[] = [
  {
    test: /stream mode/i,
    title: () => "钉钉机器人没开 Stream 模式",
    steps: () => [
      { title: "打开钉钉开发者后台，进入这个应用", link: platformLink("dingtalk") },
      { title: "在「机器人」里把消息接收模式设为 Stream 模式，然后发布机器人", detail: "Stream 模式走长连接，不需要公网地址。" },
      RESTART,
    ],
    action: () => ({ label: "打开钉钉开发者后台", href: consoles.dingtalk.href, external: true }),
  },
  {
    test: /invalid_auth|not_authed|unauthori[sz]ed|\b401\b|invalid[ _-]?(token|secret|credential)|(token|secret|credential)s? (is )?(invalid|expired|revoked)|bad credentials/i,
    title: (label) => `${label}的凭证无效或已过期`,
    steps: (name, label, channels) => [
      { title: tidy(`到${sp(consoles[name]?.label ?? `${label}后台`)}拿一份新的凭证`), link: platformLink(name) },
      { title: "在「渠道」里编辑它，粘贴新凭证，先点「测试连接」", link: { label: "去「渠道」", href: channels } },
      { title: "保存后 gateway 几秒内自动重连", detail: "不用重启。" },
    ],
    action: (_, channels) => ({ label: "更新凭证", href: channels }),
  },
  {
    test: /\b403\b|forbidden|missing_scope|permission|not allowed|access denied/i,
    title: (label) => `${label}应用的权限不够`,
    steps: (name, label) => [
      { title: tidy(`打开${sp(consoles[name]?.label ?? `${label}后台`)}，按错误里写的补上权限`), link: platformLink(name) },
      { title: "发布应用的新版本", detail: "多数平台改了权限要发布后才生效。" },
      RESTART,
    ],
    action: (name) => (consoles[name] ? { label: tidy(`打开${sp(consoles[name].label)}`), href: consoles[name].href, external: true } : { label: "复制重启命令", command: RESTART.command! }),
  },
  {
    test: /timeout|deadline exceeded|no such host|connection refused|connection reset|dial tcp|network is unreachable|\bEOF\b|tls/i,
    title: (label) => `连不上${label}的服务器`,
    steps: (_, label, channels) => [
      { title: "确认这台机器能访问外网", detail: `${label}的服务器在国外时，可能要走代理。` },
      { title: "需要代理的话，在「渠道」里给它填上「代理」", link: { label: "去「渠道」", href: channels } },
      { title: "网络恢复后 gateway 会自动重试" },
    ],
    action: (_, channels) => ({ label: "设置代理", href: channels }),
  },
];

/** A label ready to sit among Chinese text: a space on each side that is Latin. */
const sp = (label: string) => `${/^[A-Za-z0-9]/.test(label) ? " " : ""}${label}${/[A-Za-z0-9]$/.test(label) ? " " : ""}`;

/** Drops the spaces sp left at the ends or next to full-width punctuation. */
const tidy = (s: string) => s.replace(/ *([：，。「」（）]) */g, "$1").replace(/ {2,}/g, " ").trim();

function diagnose(key: string, p: ProjectStatus, name: string | undefined, raw: string, title: (who: string, label: string) => string): Problem {
  const label = sp(name ? channel(name).label : "渠道");
  const channels = href({ page: "project", project: p.id, tab: "channels" });
  const cause = causes.find((c) => c.test.test(raw));

  if (cause) {
    return {
      key,
      tone: "bad",
      project: p,
      channel: name,
      title: tidy(title(p.name + " ", label)),
      local: tidy(title("", label)),
      summary: tidy(cause.title(label)),
      steps: cause.steps(name ?? "", label, channels).map((s) => ({ ...s, title: tidy(s.title), detail: s.detail && tidy(s.detail) })),
      action: cause.action?.(name ?? "", channels),
      raw,
    };
  }

  return {
    key,
    tone: "bad",
    project: p,
    channel: name,
    title: tidy(title(p.name + " ", label)),
    local: tidy(title("", label)),
    summary: "平台返回了一个没见过的错误，原文在下面。",
    steps: [
      { title: "看原始错误，按它说的改", link: name ? { label: tidy(`${label}的接入说明`), href: channelDocs(name) } : undefined },
      { title: "凭证和代理在「渠道」里改，保存后自动重连", link: { label: "去「渠道」", href: channels } },
      { ...RESTART, title: "改的是平台那边的设置，就重启 gateway" },
    ],
    action: { label: "去「渠道」", href: channels },
    raw,
  };
}

/** Which channel an error is about: its prefix ("slack: …"), or the only one. */
function channelOf(p: ProjectStatus, raw: string): string | undefined {
  const m = /^(\w+):/.exec(raw);
  if (m && p.channels.includes(m[1])) return m[1];

  return p.channels.length === 1 ? p.channels[0] : undefined;
}

/** Everything about one project that needs the user. */
export function projectProblems(p: ProjectStatus): Problem[] {
  switch (p.state) {
    case "failed": {
      const raw = p.error ?? "出错";
      const name = channelOf(p, raw);

      return [diagnose(`${p.id}-failed`, p, name, raw, (who, label) => (name ? `${who}没连上：${label}出错` : `${who}没连上`))];
    }
    case "busy":
      return [
        {
          key: `${p.id}-busy`,
          tone: "warn",
          project: p,
          title: `${p.name} 被它自己的进程占着`,
          local: "被这个项目自己的进程占着",
          summary: "这个项目以前用 claude-bot start 单独跑的监听进程还在运行。同一个应用连两次，消息会被随机分走，所以 gateway 先不连。",
          steps: [
            { title: "在终端里停掉旧进程", command: `claude-bot -C ${shortPath(p.dir)} stop` },
            { title: "gateway 几秒内自动接手", detail: "不用重启 gateway。" },
          ],
          action: { label: "复制停止命令", command: `claude-bot -C ${shortPath(p.dir)} stop` },
        },
      ];
    case "idle":
      return [
        {
          key: `${p.id}-idle`,
          tone: "info",
          project: p,
          title: `${p.name} 还没有渠道`,
          local: "这个项目还没有渠道",
          summary: "没有渠道，消息进不来。接一个就能用，多个渠道可以同时接。",
          steps: [
            { title: "在页面上添加一个渠道", link: { label: "去添加", href: href({ page: "project", project: p.id, tab: "channels" }) } },
            { title: "或者在项目里运行 init，跟着提示走", command: `claude-bot -C ${shortPath(p.dir)} init` },
          ],
          action: { label: "添加渠道", href: href({ page: "project", project: p.id, tab: "channels" }) },
        },
      ];
    case "running":
      return (p.stopped ?? []).map((name) =>
        diagnose(`${p.id}-${name}`, p, name, p.errors?.[name] ?? "出错", (who, label) => (who ? `${who}的${label}断开了` : `${label}断开了`)),
      );
    default:
      return [];
  }
}

/** Problems that are not about one project. */
export function globalProblems(o: Overview): Problem[] {
  const out: Problem[] = [];

  if (!o.jev) {
    out.push({
      key: "jev",
      tone: "bad",
      title: "还没有 TypeSafe API key",
      summary: "每条消息都要先经过 TypeSafe 的 Jev 筛选。没有 key，gateway 不会连接任何项目。",
      steps: [
        { title: "在 TypeSafe 控制台创建一个 key", link: { label: "打开 console.typesafe.ai", href: "https://console.typesafe.ai/keys" } },
        { title: "粘贴到「共用设置」，验证后保存", link: { label: "去「共用设置」", href: href({ page: "settings" }) } },
      ],
      action: { label: "填写 key", href: href({ page: "settings" }) },
    });
  }

  return out;
}

const rank = { bad: 0, warn: 1, info: 2 };

export function allProblems(o: Overview): Problem[] {
  return [...globalProblems(o), ...o.projects.flatMap(projectProblems)].sort((a, b) => rank[a.tone] - rank[b.tone]);
}
