import { useState } from "react";
import { api } from "./api";
import { AgentPanel } from "./Agent";
import { ChannelsPanel } from "./Channels";
import { projectProblems } from "./diagnose";
import { channel, shortPath } from "./format";
import { ProblemCard } from "./Problems";
import { go, href, type Tab } from "./route";
import { projectLabel, projectTone, runLabel, runTone } from "./status";
import { TasksPanel } from "./Tasks";
import type { Overview, ProjectStatus } from "./types";
import { Banner, ChannelBadge, CopyButton, Mark, Menu, Modal, SrOnly, errorText, toast } from "./ui";

interface Props {
  project: ProjectStatus;
  overview: Overview;
  tab: Tab;
  task?: string;
  /** Reloads the overview; resolves once it is in. */
  onChanged: () => Promise<void>;
}

export function ProjectView({ project: p, overview, tab, task, onChanged }: Props) {
  const [removing, setRemoving] = useState(false);
  const tone = projectTone(p);
  const problems = projectProblems(p);
  const run = overview.runner?.runs.find((r) => r.project === p.id && r.state !== "off");

  return (
    <div className="page page-wide project">
      <header className="phead">
        <div className="phead-row">
          <h1>{p.name}</h1>
          <span className={`state-text tone-${tone}`}>
            <Mark tone={tone} live={p.state === "starting"} />
            {projectLabel(p)}
          </span>
          <span className="grow" />
          <Menu
            label="项目操作"
            items={[
              { label: "复制项目路径", onSelect: () => navigator.clipboard.writeText(p.dir).then(() => toast("已复制项目路径")) },
              { label: "从 gateway 移除…", danger: true, onSelect: () => setRemoving(true) },
            ]}
          />
        </div>
        <div className="phead-meta">
          <span className="path-group">
            <span className="path mono" title={p.dir}>
              {shortPath(p.dir)}
            </span>
            <CopyButton text={p.dir} icon label="复制路径" />
          </span>
          {p.channels.length > 0 && <span className="sep" aria-hidden />}
          <ChannelHealth project={p} />
        </div>
      </header>

      {!overview.running && p.state === "stopped" && <Banner tone="bad">gateway 没在运行。</Banner>}

      {problems.length > 0 && (
        <div className="stack">
          {problems.map((x, i) => (
            <ProblemCard key={x.key} problem={x} open={i === 0} showProject={false} />
          ))}
        </div>
      )}

      <nav className="tabs" aria-label="项目内容">
        <a className={`tab${tab === "tasks" ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "tasks" })} aria-current={tab === "tasks" ? "page" : undefined}>
          任务
          {p.pending > 0 && <span className="count">{p.pending}</span>}
        </a>
        <a className={`tab${tab === "channels" ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "channels" })} aria-current={tab === "channels" ? "page" : undefined}>
          渠道
          <span className="count count-quiet">{p.channels.length}</span>
        </a>
        {overview.runner && (
          <a className={`tab${tab === "agent" ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "agent" })} aria-current={tab === "agent" ? "page" : undefined}>
            自动处理
            {run && (
              <span className="tab-mark" title={runLabel[run.state]}>
                <Mark tone={runTone(run.state)} live={run.state === "running"} />
                <SrOnly>：{runLabel[run.state]}</SrOnly>
              </span>
            )}
          </a>
        )}
        {p.screening > 0 && (
          <span className="screening" title="刚收到、正在由 Jev 筛选的消息">
            <Mark tone="info" live />
            {p.screening} 条消息正在筛选
          </span>
        )}
      </nav>

      {tab === "tasks" && <TasksPanel project={p} selected={task} />}
      {tab === "channels" && <ChannelsPanel project={p} onChanged={onChanged} />}
      {tab === "agent" && <AgentPanel project={p} />}

      {removing && (
        <RemoveProject
          project={p}
          onClose={() => setRemoving(false)}
          // Leave once the overview no longer has the project.
          onDone={() => onChanged().then(() => go({ page: "home" }))}
        />
      )}
    </div>
  );
}

/** Confirm taking the project out of the gateway; its config goes back to the project's config.json. */
function RemoveProject({ project: p, onClose, onDone }: { project: ProjectStatus; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = () => {
    setBusy(true);
    api
      .removeProject(p.id)
      .then((r) => toast(`已移除 ${p.name}，配置写回 ${shortPath(r.config)}`))
      .then(onDone, (e) => {
        setBusy(false);
        setError(errorText(e));
      });
  };

  return (
    <Modal
      title={`从 gateway 移除 ${p.name}？`}
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-danger" onClick={confirm} disabled={busy}>
            {busy ? "移除中…" : "移除项目"}
          </button>
        </>
      }
    >
      <p>gateway 会断开这个项目的所有渠道，不再服务它。</p>
      <p className="muted">
        配置写回 <code>{shortPath(p.dir)}/.claude-bot/config.json</code>，对话记录都保留。之后可以用 <code>claude-bot start</code> 单独运行，或者再 <code>claude-bot gateway import</code> 回来。
      </p>
      {error && <p className="result result-bad">{error}</p>}
    </Modal>
  );
}

/** One chip per configured channel: connected, stopped, or not connected. */
function ChannelHealth({ project: p }: { project: ProjectStatus }) {
  if (!p.channels.length) return null;

  return (
    <span className="health">
      {p.channels.map((name) => {
        const stopped = p.stopped?.includes(name);
        const connected = p.connected?.includes(name);
        // A failed project has no channel up: each one is down, not just idle.
        const tone = stopped || p.state === "failed" ? "bad" : connected ? "ok" : p.state === "starting" ? "warn" : "off";
        const why = stopped ? "已断开" : connected ? "已连接" : p.state === "failed" ? "没连上" : p.state === "starting" ? "连接中" : "未连接";

        return (
          <a key={name} className={`chip chip-${tone}`} href={href({ page: "project", project: p.id, tab: "channels" })} title={`${channel(name).label} ${why}`}>
            <ChannelBadge name={name} size="sm" />
            {channel(name).label}
            <Mark tone={tone} />
            <SrOnly>：{why}</SrOnly>
          </a>
        );
      })}
    </span>
  );
}
