// A project's state as the page shows it: one tone, one label, one order.

import { allProblems } from "./diagnose";
import { channel, projectStateLabel } from "./format";
import type { AgentStatus, Overview, ProjectStatus, RunState, TaskState } from "./types";
import type { Tone } from "./ui";

export function projectTone(p: ProjectStatus): Tone {
  switch (p.state) {
    case "running":
      return p.stopped?.length ? "bad" : "ok";
    case "starting":
    case "busy":
      return "warn";
    case "failed":
      return "bad";
    default:
      return "off";
  }
}

export function runTone(s: RunState): Tone {
  switch (s) {
    case "running":
    case "queued":
      return "info";
    case "resting":
      return "ok";
    case "blocked":
    case "waiting":
      return "warn";
    case "failed":
      return "bad";
    default:
      return "off";
  }
}

export const runLabel: Record<RunState, string> = {
  queued: "排队中",
  running: "处理中",
  blocked: "等你确认",
  waiting: "需要你看看",
  resting: "休息中",
  failed: "启动不了",
  off: "已关闭",
};

const severity: Record<Tone, number> = { bad: 0, warn: 1, info: 2, ok: 3, off: 4 };

/** Projects that need you first; otherwise in the gateway's order. */
export function bySeverity(projects: ProjectStatus[]): ProjectStatus[] {
  return [...projects].sort((a, b) => severity[projectTone(a)] - severity[projectTone(b)]);
}

/** The project's state in words; a running project with a stopped channel says which. */
export function projectLabel(p: ProjectStatus): string {
  const stopped = p.stopped ?? [];
  if (p.state !== "running" || !stopped.length) return projectStateLabel[p.state];

  return stopped.length === 1 ? `${channel(stopped[0]).label} 已断开` : `${stopped.length} 个渠道已断开`;
}

/** The orb's state for the whole gateway. */
export function presence(o: Overview, offline: boolean): "calm" | "working" | "bad" | "off" {
  if (offline) return "off";
  if (allProblems(o).some((p) => p.tone === "bad")) return "bad";

  return o.projects.some((p) => p.pending > 0) ? "working" : "calm";
}

export const taskTone: Record<TaskState, Tone> = {
  pending: "warn",
  working: "info",
  replied: "ok",
  closed: "off",
};

/** A task still waiting on, or in the hands of, the agent; the rest are archived. */
export const isOpenTask = (s: TaskState): boolean => s === "pending" || s === "working";

/** What a task with no text is called. */
export const untitled = "（没有文字）";

/** Agent statuses with a process behind them. */
export const liveAgentStatus: ReadonlySet<AgentStatus> = new Set(["starting", "working", "blocked", "idle"]);

/** Run states the 「再跑一轮」 button can start again from. */
export const resumableRun: ReadonlySet<RunState> = new Set(["waiting", "failed", "resting"]);

/** Run states that hold one of the runner's slots. */
export const activeRun: ReadonlySet<RunState> = new Set(["running", "blocked", "waiting"]);
