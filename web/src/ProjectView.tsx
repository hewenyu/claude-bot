import { useState } from "react";
import { api } from "./api";
import { AgentPanel } from "./Agent";
import { ChannelsPanel } from "./Channels";
import { channelLabel } from "./channelKinds";
import { projectProblems } from "./diagnose";
import { shortPath } from "./format";
import { ProblemCard } from "./Problems";
import { go, href, type Tab } from "./route";
import { channelStatus, projectLabel, projectTone, runLabel, runTone } from "./status";
import { TasksPanel } from "./Tasks";
import type { Overview, ProjectStatus } from "./types";
import { Banner, ChannelBadge, ConfirmModal, CopyButton, Mark, Menu, SrOnly, StateText, copyText, toast } from "./ui";

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
          <StateText tone={tone} live={p.state === "starting"}>
            {projectLabel(p)}
          </StateText>
          <span className="grow" />
          <Menu
            label="项目操作"
            items={[
              { label: "复制项目路径", onSelect: () => void copyText(p.dir, "已复制项目路径") },
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

      {/* Taking the project out of the gateway; its config goes back to the project's config.json. */}
      {removing && (
        <ConfirmModal
          title={`从 gateway 移除 ${p.name}？`}
          confirm="移除项目"
          busyLabel="移除中…"
          onConfirm={() => api.removeProject(p.id).then((r) => toast(`已移除 ${p.name}，配置写回 ${shortPath(r.config)}`))}
          // Leave once the overview no longer has the project.
          onDone={() => onChanged().then(() => go({ page: "home" }))}
          onClose={() => setRemoving(false)}
        >
          <p>gateway 会断开这个项目的所有渠道，不再服务它。</p>
          <p className="muted">
            配置写回 <code>{shortPath(p.dir)}/.claude-bot/config.json</code>，对话记录都保留。之后可以用 <code>claude-bot start</code> 单独运行，或者再 <code>claude-bot gateway import</code> 回来。
          </p>
        </ConfirmModal>
      )}
    </div>
  );
}

/** One chip per configured channel: connected, stopped, or not connected. */
function ChannelHealth({ project: p }: { project: ProjectStatus }) {
  if (!p.channels.length) return null;

  return (
    <span className="health">
      {p.channels.map((name) => {
        const { tone, label: why } = channelStatus(p, name);
        const label = channelLabel(name);

        return (
          <a key={name} className={`chip chip-${tone}`} href={href({ page: "project", project: p.id, tab: "channels" })} title={`${label} ${why}`}>
            <ChannelBadge name={name} size="sm" />
            {label}
            <Mark tone={tone} />
            <SrOnly>：{why}</SrOnly>
          </a>
        );
      })}
    </span>
  );
}
