// Shapes of the gateway's API (cmd/claude-bot/gateway_web*.go). Each one
// names the Go type it mirrors.

/** projectState in cmd/claude-bot/gateway.go. */
export type ProjectState = "running" | "starting" | "busy" | "failed" | "idle" | "stopped";

/** projectStatus in cmd/claude-bot/gateway_cmd.go. */
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

/** overviewResponse in cmd/claude-bot/gateway_web.go (gatewayStatus in gateway_cmd.go, plus self and runner). */
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
  /** Absent when the gateway runs no agents. */
  runner?: RunnerOverview;
}

// The runner (internal/runner): Claude Code with /bot in each project that has it on.

/** runner.State in internal/runner/runner.go. */
export type RunState = "queued" | "running" | "blocked" | "waiting" | "resting" | "failed" | "off";

/** agent.Status in internal/agent/status.go. */
export type AgentStatus = "starting" | "working" | "blocked" | "idle" | "exited" | "lost";

/** agent.Info in internal/agent/agent.go (with its Activity), the fields the page reads. */
export interface AgentInfo {
  id: string;
  name: string;
  cwd: string;
  status: AgentStatus;
  detail?: string;
  title?: string;
  turns: number;
  /** How the latest turn ended (agent.End): only "stop" is the agent finishing it itself. */
  ended?: "stop" | "interrupt" | "declined";
  last_message?: string;
  subagents?: number;
  created_at: string;
  updated_at: string;
}

/** runView in cmd/claude-bot/gateway_web_runner.go: runner.Run (internal/runner/runner.go) and its agent. */
export interface Run {
  project: number;
  name: string;
  dir: string;
  state: RunState;
  detail?: string;
  error?: string;
  agent_id?: string;
  pending: number;
  summary?: string;
  starts: number;
  /** The round's agent said it is done (`claude-bot finish`). */
  finished?: boolean;
  /** How often its Stop hook kept it from stopping before it finished. */
  blocks?: number;
  updated_at: string;
  log?: { at: string; text: string }[];
  agent?: AgentInfo;
}

/** runnerOverview in cmd/claude-bot/gateway_web_runner.go. */
export interface RunnerOverview {
  max_parallel: number;
  runs: Run[];
}

/** taskState in cmd/claude-bot/gateway_web_tasks.go. */
export type TaskState = "pending" | "working" | "replied" | "closed";

/** taskSummary in cmd/claude-bot/gateway_web_tasks.go. */
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

/** channel.User in internal/channel/channel.go. */
export interface User {
  id: string;
  name?: string;
  mention?: string;
}

/** inbox.Entry in internal/inbox/task.go. */
export interface Entry {
  kind: "user" | "bot";
  message?: string;
  sender?: User;
  reply_to?: string;
  at: string;
  text?: string;
  file?: string;
}

/** taskDetail in cmd/claude-bot/gateway_web_tasks.go. */
export interface TaskDetail extends TaskSummary {
  handled: number;
  thread_id?: string;
  log: Entry[];
}

/** channelField in cmd/claude-bot/gateway_web_channels.go. */
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

/** channelType in cmd/claude-bot/gateway_web_channels.go. */
export interface ChannelType {
  name: string;
  label: string;
  fields: Field[];
  note?: string;
  add_note?: string;
  /** Why this project cannot add the channel. */
  unavailable?: string;
}

/** secretView in cmd/claude-bot/gateway_web_channels.go. */
export interface SecretView {
  set: boolean;
  hint?: string;
}

/** A channel field's value is a secret, masked, rather than plain text. */
export function isSecret(v: unknown): v is SecretView {
  return typeof v === "object" && v !== null && "set" in v;
}

/** A secret in words: "已设置 …1234", "已设置" without a hint, else unset. */
export function secretText(v: SecretView, unset = "未设置"): string {
  if (!v.set) return unset;

  return v.hint ? `已设置 ${v.hint}` : "已设置";
}

/** channelView in cmd/claude-bot/gateway_web_channels.go. */
export interface ChannelView {
  name: string;
  label: string;
  fields: Record<string, string | SecretView>;
  app_key: string;
  updated_at: string;
}

/** testResult in cmd/claude-bot/gateway_web_channels.go. */
export interface TestResult {
  ok: boolean;
  identity?: string;
  error?: string;
}

/** githubTokenView in cmd/claude-bot/gateway_web_github.go. */
export interface GitHubToken {
  /** "" is the default token. */
  name: string;
  token: SecretView;
  projects: string[];
}

/** githubProject in cmd/claude-bot/gateway_web_github.go. */
export interface GitHubProject {
  id: number;
  name: string;
  repo: string;
  token_name?: string;
}

/** settingsView in cmd/claude-bot/gateway_web_settings.go. */
export interface Settings {
  typesafe_key: SecretView;
  github_tokens: GitHubToken[];
  github_projects: GitHubProject[];
}
