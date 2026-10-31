import type { ProjectState, TaskState } from "./types";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function clock(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "刚刚", "5 分钟前", "3 小时前", "昨天 14:03", "10月28日 09:12", "2025年3月1日". */
export function ago(iso: string, now = Date.now()): string {
  const d = new Date(iso);
  const diff = now - d.getTime();

  if (diff < MINUTE) return "刚刚";
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)} 分钟前`;

  const today = new Date(now);
  today.setHours(0, 0, 0, 0);

  if (d >= today) return diff < 6 * HOUR ? `${Math.floor(diff / HOUR)} 小时前` : `今天 ${clock(d)}`;
  if (d.getTime() >= today.getTime() - DAY) return `昨天 ${clock(d)}`;
  if (d.getFullYear() === today.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日 ${clock(d)}`;

  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日`;
}

/** Full local time, for tooltips. */
export function fullTime(iso: string): string {
  const d = new Date(iso);

  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${clock(d)}:${pad(d.getSeconds())}`;
}

export function uptime(iso: string, now = Date.now()): string {
  const diff = now - new Date(iso).getTime();
  if (diff < HOUR) return `${Math.max(1, Math.floor(diff / MINUTE))} 分钟`;
  if (diff < DAY) return `${Math.floor(diff / HOUR)} 小时`;

  return `${Math.floor(diff / DAY)} 天`;
}

export const projectStateLabel: Record<ProjectState, string> = {
  running: "运行中",
  starting: "连接中",
  busy: "被占用",
  failed: "出错",
  idle: "没有渠道",
  stopped: "未运行",
};

export const taskStateLabel: Record<TaskState, string> = {
  pending: "待处理",
  working: "处理中",
  replied: "已回复",
  closed: "已关闭",
};

const channelLabels: Record<string, string> = {
  lark: "飞书",
  dingtalk: "钉钉",
  telegram: "Telegram",
  discord: "Discord",
  slack: "Slack",
  github: "GitHub",
};

export function channel(name: string): { label: string } {
  return { label: channelLabels[name] ?? name };
}

/** Shortens a home-relative path for display. */
export function shortPath(dir: string): string {
  return dir.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}
