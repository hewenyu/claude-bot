// The gateway's API. Every call carries the page token, which `claude-bot
// gateway open` hands over in the URL fragment (never sent to the server
// as part of a URL) and the page keeps in localStorage.

import type {
  ChannelType,
  ChannelView,
  Overview,
  Settings,
  TaskDetail,
  TaskSummary,
  TestResult,
} from "./types";

const TOKEN_KEY = "claude-bot-gateway-token";

/** Takes the token out of the URL fragment, if it is there. */
export function adoptToken(): void {
  const m = /(?:^#|&)token=([0-9a-f]+)/.exec(location.hash);
  if (m) {
    localStorage.setItem(TOKEN_KEY, m[1]);
    history.replaceState(null, "", location.pathname + "#/");
  }
}

export function hasToken(): boolean {
  return !!localStorage.getItem(TOKEN_KEY);
}

/** The gateway refused the token, or there is none. */
export class AuthError extends Error {}

/** The gateway cannot be reached. */
export class OfflineError extends Error {}

/** The gateway answered with an error it explains. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: {
        Authorization: `Bearer ${localStorage.getItem(TOKEN_KEY) ?? ""}`,
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new OfflineError("连不上 gateway");
  }

  if (res.status === 401) throw new AuthError("需要访问令牌");
  if (res.status === 204) return undefined as T;

  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError((data as { error?: string }).error ?? res.statusText, res.status);

  return data as T;
}

const p = (id: number) => `/api/projects/${id}`;

export const api = {
  overview: () => call<Overview>("GET", "/api/overview"),
  channelTypes: (project: number) => call<ChannelType[]>("GET", `${p(project)}/channel-types`),
  tasks: (project: number) => call<TaskSummary[]>("GET", `${p(project)}/tasks`),
  task: (project: number, id: string) => call<TaskDetail>("GET", `${p(project)}/tasks/${encodeURIComponent(id)}`),
  channels: (project: number) => call<ChannelView[]>("GET", `${p(project)}/channels`),
  saveChannel: (project: number, name: string, fields: Record<string, string>) =>
    call<ChannelView>("PUT", `${p(project)}/channels/${name}`, { fields }),
  testChannel: (project: number, name: string, fields: Record<string, string>) =>
    call<TestResult>("POST", `${p(project)}/channels/${name}/test`, { fields }),
  removeChannel: (project: number, name: string) => call<void>("DELETE", `${p(project)}/channels/${name}`),
  removeProject: (project: number) => call<{ config: string }>("DELETE", p(project)),
  settings: () => call<Settings>("GET", "/api/settings"),
  saveSettings: (s: { typesafe_key?: string; github_token?: string }) => call<Settings>("PUT", "/api/settings", s),
  saveGitHubToken: (name: string, token: string, projects: number[]) =>
    call<Settings>("PUT", `/api/github-tokens/${encodeURIComponent(name)}`, { token, projects }),
  removeGitHubToken: (name: string) => call<Settings>("DELETE", `/api/github-tokens/${encodeURIComponent(name)}`),
};
