// Shapes of the gateway's API (cmd/claude-bot/gateway_web.go).

export type ProjectState = "running" | "starting" | "busy" | "failed" | "idle" | "stopped";

export interface ProjectStatus {
  id: number;
  name: string;
  dir: string;
  state: ProjectState;
  error?: string;
  channels: string[];
  connected?: string[];
  stopped?: string[];
  errors?: Record<string, string>;
  pending: number;
  screening: number;
}

export interface Overview {
  home: string;
  running: boolean;
  pid?: number;
  version?: string;
  started_at?: string;
  jev: boolean;
  github_token: boolean;
  projects: ProjectStatus[];
  self: string;
}

export type TaskState = "pending" | "working" | "replied" | "closed";

export interface TaskSummary {
  id: string;
  channel: string;
  chat_id: string;
  chat_type: "p2p" | "group";
  state: TaskState;
  title: string;
  senders: string[];
  entries: number;
  replies: number;
  unhandled: number;
  created_at: string;
  updated_at: string;
  link?: string;
}

export interface User {
  id: string;
  name?: string;
  mention?: string;
}

export interface Entry {
  kind: "user" | "bot";
  message?: string;
  sender?: User;
  reply_to?: string;
  at: string;
  text?: string;
  file?: string;
}

export interface TaskDetail extends TaskSummary {
  handled: number;
  thread_id?: string;
  log: Entry[];
}

export interface Field {
  key: string;
  label: string;
  secret?: boolean;
  required?: boolean;
  /** A choice of options, "" being the default. */
  select?: boolean;
  options?: string[];
  placeholder?: string;
  /** The value a new channel starts with. */
  default?: string;
}

export interface ChannelType {
  name: string;
  label: string;
  fields: Field[];
  note?: string;
  add_note?: string;
  /** Why this project cannot add the channel. */
  unavailable?: string;
}

export interface SecretView {
  set: boolean;
  hint?: string;
}

export interface ChannelView {
  name: string;
  label: string;
  fields: Record<string, string | SecretView>;
  app_key: string;
  updated_at: string;
}

export interface TestResult {
  ok: boolean;
  identity?: string;
  error?: string;
}

export interface GitHubToken {
  /** "" is the default token. */
  name: string;
  token: SecretView;
  projects: string[];
}

export interface GitHubProject {
  id: number;
  name: string;
  repo: string;
  token_name?: string;
}

export interface Settings {
  typesafe_key: SecretView;
  github_tokens: GitHubToken[];
  github_projects: GitHubProject[];
}
