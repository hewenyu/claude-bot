import { useState } from "react";
import { api } from "./api";
import { projectTone } from "./App";
import { ChannelsPanel } from "./Channels";
import { channel, projectStateLabel, shortPath } from "./format";
import { go, href } from "./route";
import { TasksPanel } from "./Tasks";
import type { Overview, ProjectStatus } from "./types";
import { Banner, ChannelBadge, Command, CopyButton, Dot, Modal, errorText, toast } from "./ui";

interface Props {
  project: ProjectStatus;
  overview: Overview;
  tab: "tasks" | "channels";
  task?: string;
  /** Reloads the overview; resolves once it is in. */
  onChanged: () => Promise<void>;
}

export function ProjectView({ project: p, overview, tab, task, onChanged }: Props) {
  const [removing, setRemoving] = useState(false);

  return (
    <div className="project">
      <header className="project-head">
        <div className="project-title">
          <h1>{p.name}</h1>
          <span className={`state state-${projectTone(p)}`}>
            <Dot tone={projectTone(p)} pulse={p.state === "starting"} />
            {projectStateLabel[p.state]}
          </span>
          <span className="grow" />
          <button type="button" className="btn btn-danger-outline" onClick={() => setRemoving(true)}>
            移除项目
          </button>
        </div>
        <div className="project-meta">
          <span className="path" title={p.dir}>
            {shortPath(p.dir)}
          </span>
          <CopyButton text={p.dir} label="复制路径" />
          <span className="sep" aria-hidden />
          <ChannelHealth project={p} />
        </div>
      </header>

      <StateNotice project={p} overview={overview} />

      <nav className="tabs" aria-label="项目内容">
        <a className={`tab${tab === "tasks" ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "tasks" })}>
          任务
          {p.pending > 0 && <span className="pill pill-accent">{p.pending} 待处理</span>}
          {p.screening > 0 && <span className="pill" title="刚收到、正在由 Jev 筛选的消息">{p.screening} 筛选中</span>}
        </a>
        <a className={`tab${tab === "channels" ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "channels" })}>
          渠道 <span className="pill">{p.channels.length}</span>
        </a>
      </nav>

      {tab === "tasks" ? <TasksPanel project={p} selected={task} /> : <ChannelsPanel project={p} onChanged={onChanged} />}

      {removing && (
        <RemoveProject
          project={p}
          onClose={() => setRemoving(false)}
          // Leave once the overview no longer has the project: going home
          // with the old one would open the first project, this one again.
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
      {error && <p className="test test-bad">{error}</p>}
    </Modal>
  );
}

/** One chip per configured channel: connected, stopped, or not connected. */
function ChannelHealth({ project: p }: { project: ProjectStatus }) {
  if (!p.channels.length) return <span className="muted">没有渠道</span>;

  return (
    <span className="health">
      {p.channels.map((name) => {
        const stopped = p.stopped?.includes(name);
        const connected = p.connected?.includes(name);
        const tone = stopped ? "bad" : connected ? "ok" : "off";
        const why = stopped ? `已停止：${p.errors?.[name] ?? "出错"}` : connected ? "已连接" : "未连接";

        return (
          <span key={name} className={`chip chip-${tone}`} title={`${channel(name).label} ${why}`}>
            <ChannelBadge name={name} size="sm" />
            {channel(name).label}
            <Dot tone={tone} />
          </span>
        );
      })}
    </span>
  );
}

/** Why the project is not served as it should be, and what to do. */
function StateNotice({ project: p, overview }: { project: ProjectStatus; overview: Overview }) {
  const stopped = p.stopped ?? [];

  switch (p.state) {
    case "busy":
      return (
        <Banner tone="warn">
          <p>
            <strong>这个项目自己的监听进程还在运行</strong>，gateway 不会再连一次（同一个应用连两次，消息会被随机分走）。在终端里停掉它，gateway 几秒内自动接手：
          </p>
          <Command>{`claude-bot -C ${p.dir} stop`}</Command>
        </Banner>
      );
    case "failed":
      return (
        <Banner tone="bad">
          <p>
            <strong>没能连上这个项目：</strong>
            {p.error}
          </p>
          <p className="muted">改好配置（比如在「渠道」里更新凭证）后，gateway 会自动重试。</p>
        </Banner>
      );
    case "idle":
      return (
        <Banner tone="info">
          <p>这个项目还没有渠道。在「渠道」里添加，或者在项目里运行：</p>
          <Command>{`claude-bot -C ${p.dir} init`}</Command>
        </Banner>
      );
    case "stopped":
      return overview.running ? null : <Banner tone="bad">gateway 没在运行。</Banner>;
    default:
      return stopped.length ? (
        <Banner tone="bad">
          {stopped.map((name) => (
            <p key={name}>
              <strong>{channel(name).label} 已停止：</strong>
              {p.errors?.[name] ?? "出错"}。其他渠道照常工作；在「渠道」里更新凭证后会自动重连。
            </p>
          ))}
        </Banner>
      ) : null;
  }
}
