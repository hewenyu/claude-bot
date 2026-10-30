import { useMemo, useState } from "react";
import { api, ApiError } from "./api";
import { ago, channel, fullTime, taskStateLabel } from "./format";
import { useNow, usePoll } from "./hooks";
import { Markdown } from "./markdown";
import { go, href } from "./route";
import type { Entry, ProjectStatus, TaskDetail, TaskState, TaskSummary } from "./types";
import { Banner, ChannelBadge, CopyButton, Dot, Empty, errorText } from "./ui";

type Filter = "all" | "open" | "done";

const filters: { key: Filter; label: string; match: (s: TaskState) => boolean }[] = [
  { key: "all", label: "全部", match: () => true },
  { key: "open", label: "未完成", match: (s) => s === "pending" || s === "working" },
  { key: "done", label: "已完成", match: (s) => s === "replied" || s === "closed" },
];

const stateTone: Record<TaskState, "warn" | "info" | "ok" | "off"> = {
  pending: "warn",
  working: "info",
  replied: "ok",
  closed: "off",
};

export function TasksPanel({ project, selected }: { project: ProjectStatus; selected?: string }) {
  const tasks = usePoll(() => api.tasks(project.id), 5000, `tasks-${project.id}`);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const now = useNow();

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = filters.find((f) => f.key === filter)!.match;

    return (tasks.data ?? []).filter(
      (t) => match(t.state) && (!q || t.title.toLowerCase().includes(q) || t.senders.some((s) => s.toLowerCase().includes(q)) || t.id.includes(q)),
    );
  }, [tasks.data, filter, query]);

  const counts = useMemo(() => {
    const all = tasks.data ?? [];

    return Object.fromEntries(filters.map((f) => [f.key, all.filter((t) => f.match(t.state)).length])) as Record<Filter, number>;
  }, [tasks.data]);

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
            {filters.map((f) => (
              <button key={f.key} type="button" role="tab" aria-selected={filter === f.key} className={filter === f.key ? "on" : ""} onClick={() => setFilter(f.key)}>
                {f.label}
                <span className="seg-count">{counts[f.key]}</span>
              </button>
            ))}
          </div>
          <input className="search" type="search" placeholder="搜索标题、发送人" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="搜索任务" />
        </div>

        {!tasks.data && <ListSkeleton />}
        {tasks.data && !shown.length && (
          <Empty title="没有符合条件的任务">
            <button type="button" className="btn btn-ghost" onClick={() => (setFilter("all"), setQuery(""))}>
              清除筛选
            </button>
          </Empty>
        )}
        <ul>
          {shown.map((t) => (
            <TaskRow key={t.id} task={t} project={project.id} active={t.id === selected} now={now} />
          ))}
        </ul>
      </section>

      <section className="conversation" aria-label="对话">
        {selected ? <Conversation project={project.id} id={selected} /> : <Empty title="选一个任务，查看完整对话" icon={<span className="big-icon">☰</span>} />}
      </section>
    </div>
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
