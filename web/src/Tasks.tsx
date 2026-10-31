import { useMemo, useState } from "react";
import { api, ApiError } from "./api";
import { ago, channel, fullTime, taskStateLabel } from "./format";
import { useNow, usePoll } from "./hooks";
import { Markdown } from "./markdown";
import { go, href } from "./route";
import type { Entry, ProjectStatus, TaskDetail, TaskState, TaskSummary } from "./types";
import { Banner, ChannelBadge, CopyButton, Dot, Empty, errorText } from "./ui";

/**
 * The page shows the open tasks; handled ones are archived on their own and
 * come back as soon as someone follows up (the task is pending again).
 */
type View = "open" | "archived";

const archived = (s: TaskState) => s === "replied" || s === "closed";

const views: { key: View; label: string; match: (s: TaskState) => boolean }[] = [
  { key: "open", label: "进行中", match: (s) => !archived(s) },
  { key: "archived", label: "已归档", match: archived },
];

const matches = (t: TaskSummary, q: string) =>
  !q || t.title.toLowerCase().includes(q) || t.senders.some((s) => s.toLowerCase().includes(q)) || t.id.includes(q);

const stateTone: Record<TaskState, "warn" | "info" | "ok" | "off"> = {
  pending: "warn",
  working: "info",
  replied: "ok",
  closed: "off",
};

export function TasksPanel({ project, selected }: { project: ProjectStatus; selected?: string }) {
  const tasks = usePoll(() => api.tasks(project.id), 5000, `tasks-${project.id}`);
  const [view, setView] = useState<View>("open");
  const [query, setQuery] = useState("");
  const now = useNow();

  const { shown, counts, found } = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = tasks.data ?? [];
    const of = (v: View) => all.filter((t) => views.find((x) => x.key === v)!.match(t.state));
    const byView = { open: of("open"), archived: of("archived") };
    const found = { open: byView.open.filter((t) => matches(t, q)), archived: byView.archived.filter((t) => matches(t, q)) };

    return { shown: found[view], counts: { open: byView.open.length, archived: byView.archived.length }, found };
  }, [tasks.data, view, query]);

  // With no task picked, the page shows the latest open conversation.
  const current = selected ?? (view === "open" && !query.trim() ? shown[0]?.id : undefined);
  const other: View = view === "open" ? "archived" : "open";

  if (tasks.error && !tasks.data) {
    return <Banner tone="bad">读不到这个项目的任务：{errorText(tasks.error)}</Banner>;
  }

  if (tasks.data && !tasks.data.length) {
    return (
      <div className="panel-empty">
        <Empty title="还没有任务" icon={<span className="big-icon">✉</span>}>
          <p>私聊 bot、在群里 @ 它，或者在接了 GitHub 渠道的仓库里开 issue，消息经 Jev 归成一件件事后会出现在这里。</p>
          {project.screening > 0 && (
            <p>
              <Dot tone="info" pulse /> 有 {project.screening} 条消息正在筛选。
            </p>
          )}
        </Empty>
      </div>
    );
  }

  return (
    <div className="tasks">
      <section className="task-list" aria-label="任务列表">
        <div className="task-tools">
          <div className="segmented" role="tablist">
            {views.map((v) => (
              <button key={v.key} type="button" role="tab" aria-selected={view === v.key} className={view === v.key ? "on" : ""} onClick={() => setView(v.key)}>
                {v.label}
                <span className="seg-count">{counts[v.key]}</span>
              </button>
            ))}
          </div>
          <input className="search" type="search" placeholder="搜索标题、发送人" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="搜索任务" />
        </div>

        {view === "archived" && shown.length > 0 && <p className="task-note">回复过或标记过的任务自动归档在这里；有人追问时会回到「进行中」。</p>}
        {!tasks.data && <ListSkeleton />}
        {tasks.data && !shown.length && (
          <EmptyView view={view} query={query.trim()} elsewhere={found[other].length} archivedCount={counts.archived} onSwitch={() => setView(other)} onClear={() => setQuery("")} />
        )}
        <ul>
          {shown.map((t) => (
            <TaskRow key={t.id} task={t} project={project.id} active={t.id === current} now={now} />
          ))}
        </ul>
      </section>

      <section className="conversation" aria-label="对话">
        {current ? <Conversation project={project.id} id={current} /> : <Empty title="选一个任务，查看完整对话" icon={<span className="big-icon">☰</span>} />}
      </section>
    </div>
  );
}

interface EmptyViewProps {
  view: View;
  query: string;
  elsewhere: number; // matches in the other view
  archivedCount: number;
  onSwitch: () => void;
  onClear: () => void;
}

function EmptyView({ view, query, elsewhere, archivedCount, onSwitch, onClear }: EmptyViewProps) {
  const there = view === "open" ? "已归档" : "进行中";

  if (query) {
    return (
      <Empty title="没有符合条件的任务">
        {elsewhere > 0 && (
          <button type="button" className="btn btn-ghost" onClick={onSwitch}>
            {there}里有 {elsewhere} 件
          </button>
        )}
        <button type="button" className="btn btn-ghost" onClick={onClear}>
          清除搜索
        </button>
      </Empty>
    );
  }

  if (view === "open") {
    return (
      <Empty title="没有进行中的任务" icon={<span className="big-icon">✓</span>}>
        <p>{archivedCount > 0 ? `处理完的 ${archivedCount} 件已自动归档。` : ""}有新消息或追问时，任务会出现在这里。</p>
        {archivedCount > 0 && (
          <button type="button" className="btn btn-ghost" onClick={onSwitch}>
            查看已归档
          </button>
        )}
      </Empty>
    );
  }

  return (
    <Empty title="还没有归档的任务">
      <p>回复过或标记过的任务会自动归档到这里。</p>
    </Empty>
  );
}

function TaskRow({ task: t, project, active, now }: { task: TaskSummary; project: number; active: boolean; now: number }) {
  return (
    <li>
      <a className={`task-row${active ? " active" : ""}`} href={href({ page: "project", project, tab: "tasks", task: t.id })} aria-current={active ? "true" : undefined}>
        <div className="task-row-top">
          <span className={`tag tag-${stateTone[t.state]}`}>
            {t.state === "working" && <Dot tone="info" pulse />}
            {taskStateLabel[t.state]}
          </span>
          <span className="task-title">{t.title || "（没有文字）"}</span>
        </div>
        <div className="task-row-meta">
          <ChannelBadge name={t.channel} size="sm" />
          <span>{t.chat_type === "group" ? "群聊" : t.channel === "github" ? "GitHub" : "私聊"}</span>
          {t.senders.length > 0 && <span className="ellipsis">· {t.senders.slice(0, 2).join("、")}{t.senders.length > 2 ? ` 等 ${t.senders.length} 人` : ""}</span>}
          <span className="grow" />
          {t.replies > 0 && <span title={`bot 回复了 ${t.replies} 次`}>↩ {t.replies}</span>}
          <time dateTime={t.updated_at} title={fullTime(t.updated_at)}>
            {ago(t.updated_at, now)}
          </time>
        </div>
      </a>
    </li>
  );
}

function ListSkeleton() {
  return (
    <ul aria-hidden>
      {[0, 1, 2, 3].map((i) => (
        <li key={i} className="task-row skeleton">
          <div className="sk sk-line" />
          <div className="sk sk-short" />
        </li>
      ))}
    </ul>
  );
}

/** Consecutive user entries of one message, as one bubble. */
interface Turn {
  kind: "user" | "bot";
  entries: Entry[];
  index: number; // of the first entry
}

function turns(log: Entry[]): Turn[] {
  const out: Turn[] = [];

  log.forEach((e, index) => {
    const last = out[out.length - 1];
    if (last && e.kind === "user" && last.kind === "user" && last.entries[0].message === e.message) {
      last.entries.push(e);
    } else {
      out.push({ kind: e.kind, entries: [e], index });
    }
  });

  return out;
}

function Conversation({ project, id }: { project: number; id: string }) {
  const task = usePoll(() => api.task(project, id), 5000, `task-${project}-${id}`);
  const now = useNow();

  if (task.error && !task.data) {
    const gone = task.error instanceof ApiError && task.error.status === 404;

    return (
      <Empty title={gone ? "找不到这个任务" : "读不到这个任务"}>
        <p>{gone ? "它可能已经被清理掉了。" : errorText(task.error)}</p>
        <button type="button" className="btn btn-ghost" onClick={() => go({ page: "project", project, tab: "tasks" })}>
          返回列表
        </button>
      </Empty>
    );
  }

  if (!task.data) return <div className="conv-loading" aria-busy="true" />;

  const t: TaskDetail = task.data;
  const ts = turns(t.log);
  const firstNew = ts.findIndex((x) => x.kind === "user" && x.index >= t.handled);

  return (
    <div className="conv">
      <header className="conv-head">
        <div className="conv-title">
          <h2>{t.title || "（没有文字）"}</h2>
          <span className={`tag tag-${stateTone[t.state]}`}>{taskStateLabel[t.state]}</span>
        </div>
        <div className="conv-meta">
          <ChannelBadge name={t.channel} size="sm" />
          <span>
            {channel(t.channel).label} · {t.chat_type === "group" ? "群聊" : "私聊"}
          </span>
          <span className="mono" title="会话 id">
            {t.chat_id}
          </span>
          <span>· 创建于 {ago(t.created_at, now)}</span>
          <span className="grow" />
          {t.link && (
            <a className="btn btn-ghost btn-xs" href={t.link} target="_blank" rel="noreferrer noopener">
              在 GitHub 打开 ↗
            </a>
          )}
          <CopyButton text={t.id} label="复制任务 id" />
        </div>
      </header>

      <ol className="timeline">
        {ts.map((turn, i) => (
          <li key={turn.index}>
            {i === firstNew && <div className="new-divider">以下 {t.unhandled} 段还没处理</div>}
            <Bubble turn={turn} now={now} />
          </li>
        ))}
      </ol>
    </div>
  );
}

function Bubble({ turn, now }: { turn: Turn; now: number }) {
  const first = turn.entries[0];

  if (turn.kind === "bot") {
    return (
      <div className="bubble-row bot">
        <div className="bubble bubble-bot">
          <div className="bubble-head">
            <span className="who">bot</span>
            <time dateTime={first.at} title={fullTime(first.at)}>
              {ago(first.at, now)}
            </time>
          </div>
          <Markdown text={first.text ?? ""} />
        </div>
      </div>
    );
  }

  const who = first.sender?.name || first.sender?.id || "用户";
  const text = turn.entries.filter((e) => !e.file).map((e) => e.text ?? "").join("");
  const files = turn.entries.filter((e) => e.file);

  return (
    <div className="bubble-row user">
      <div className="avatar" aria-hidden>
        {who.slice(0, 1).toUpperCase()}
      </div>
      <div className="bubble bubble-user">
        <div className="bubble-head">
          <span className="who" title={first.sender?.id}>
            {who}
          </span>
          <time dateTime={first.at} title={fullTime(first.at)}>
            {ago(first.at, now)}
          </time>
          {first.reply_to && (
            <span className="muted" title={first.reply_to}>
              · 回复了一条消息
            </span>
          )}
        </div>
        {text && <div className="plain">{text}</div>}
        {files.map((f) => (
          <div key={f.file} className="file" title={f.file}>
            <span aria-hidden>📎</span>
            <span className="file-name">{f.file!.split("/").pop()}</span>
            <CopyButton text={f.file!} label="复制路径" />
          </div>
        ))}
      </div>
    </div>
  );
}
