// A project's state as the page shows it: one tone, one label, one order.

import { allProblems } from "./diagnose";
import { channel, projectStateLabel } from "./format";
import type { Overview, ProjectStatus } from "./types";
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
