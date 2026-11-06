import { useEffect, useMemo, useState } from "react";
import { api, ApiError } from "./api";
import { channelFacts, channelLabel } from "./channelKinds";
import { ago, errorText, fullTime, taskStateLabel } from "./format";
import { POLL_SLOW, useNow, usePoll } from "./hooks";
import { Icon } from "./icons";
import { Markdown } from "./markdown";
import { go, href } from "./route";
import { isOpenTask, taskTone, untitled } from "./status";
import type { Entry, ProjectStatus, TaskDetail, TaskSummary } from "./types";
import { Banner, ChannelBadge, CopyButton, Empty, Mark, SrOnly } from "./ui";

/**
 * The page shows the open tasks; handled ones are archived on their own and
 * come back as soon as someone follows up (the task is pending again).
 */
type View = "open" | "archived";

const views: { key: View; label: string }[] = [
  { key: "open", label: "进行中" },
  { key: "archived", label: "已归档" },
];

const viewOf = (t: TaskSummary): View => (isOpenTask(t.state) ? "open" : "archived");

const matches = (t: TaskSummary, q: string) =>
  !q || t.title.toLowerCase().includes(q) || t.senders.some((s) => s.toLowerCase().includes(q)) || t.id.includes(q);

/** A channel whose threads are public issues and PRs (GitHub's), not group chats or DMs. */
const onIssues = (t: TaskSummary) => !!channelFacts(t.channel)?.issues;

const chatKind = (t: TaskSummary) => (onIssues(t) ? channelLabel(t.channel) : t.chat_type === "group" ? "群聊" : "私聊");

/** An entry that is a saved attachment. */
const isFile = (e: Entry): e is Entry & { file: string } => !!e.file;

/** Why nothing has come in, for a project whose messages cannot arrive yet; the banner above says how to fix it. */
const noTasksYet: Partial<Record<ProjectStatus["state"], string>> = {
  starting: "渠道正在连接，连上后新消息会出现在这里。",
  failed: "消息现在进不来。按上面的步骤修好后，新消息会出现在这里。",
  busy: "消息现在被旧进程收走了。按上面的步骤停掉它后，新消息会出现在这里。",
  idle: "接上渠道后，新消息会出现在这里。",
  stopped: "gateway 没在运行，消息进不来。",
};

export function TasksPanel({ project, selected }: { project: ProjectStatus; selected?: string }) {
  const tasks = usePoll(() => api.tasks(project.id), POLL_SLOW, `tasks-${project.id}`);
  const [view, setView] = useState<View>("open");
  const [query, setQuery] = useState("");
  const now = useNow();

  const { shown, counts, found } = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = tasks.data ?? [];
    const byView = { open: all.filter((t) => isOpenTask(t.state)), archived: all.filter((t) => !isOpenTask(t.state)) };
    const found = { open: byView.open.filter((t) => matches(t, q)), archived: byView.archived.filter((t) => matches(t, q)) };

    return { shown: found[view], counts: { open: byView.open.length, archived: byView.archived.length }, found };
  }, [tasks.data, view, query]);

  // A link to an archived task opens the archive, so its row shows as picked.
  // Once per link: switching views by hand afterwards sticks.
  const loaded = !!tasks.data;
  useEffect(() => {
    const t = tasks.data?.find((x) => x.id === selected);
    if (t) setView(viewOf(t));
  }, [selected, loaded]); // not tasks.data: each poll would undo a switch by hand

  // With no task picked, the page shows the latest open conversation.
  const current = selected ?? (view === "open" && !query.trim() ? shown[0]?.id : undefined);
  const other: View = view === "open" ? "archived" : "open";

  if (tasks.error && !tasks.data) {
    return <Banner tone="bad">读不到这个项目的任务：{errorText(tasks.error)}</Banner>;
  }

  if (tasks.data && !tasks.data.length) {
    return (
      <div className="panel-empty">
        <Empty title="还没有任务" icon={<Icon name="inbox" />}>
          <p>{noTasksYet[project.state] ?? "私聊 bot、在群里 @ 它，或者在接了 GitHub 渠道的仓库里开 issue，消息经 Jev 归成一件件事后会出现在这里。"}</p>
          {project.screening > 0 && (
            <p>
              <Mark tone="info" live /> 有 {project.screening} 条消息正在筛选。
            </p>
          )}
        </Empty>
      </div>
    );
  }

  return (
    <div className={`tasks${!current && !shown.length ? " solo" : ""}`}>
      <section className="task-list" aria-label="任务列表">
        <div className="task-tools">
          <div className="segmented" role="group" aria-label="显示哪些任务">
            {views.map((v) => (
              <button key={v.key} type="button" aria-pressed={view === v.key} className={view === v.key ? "on" : ""} onClick={() => setView(v.key)}>
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

      {/* Nothing to pick: the list says why, and an empty pane beside it would only repeat it. */}
      {(current || shown.length > 0) && (
        <section className="conversation" aria-label="对话">
          {current ? <Conversation project={project.id} id={current} /> : <Empty title="选一个任务，查看完整记录" icon={<Icon name="chat" />} />}
        </section>
      )}
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
      <Empty title="没有进行中的任务" icon={<Icon name="check" />}>
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
  const tone = taskTone[t.state];

  return (
    <li>
      <a className={`task-row${active ? " active" : ""}`} href={href({ page: "project", project, tab: "tasks", task: t.id })} aria-current={active ? "true" : undefined}>
        <span className="task-row-top">
          <Mark tone={tone} live={t.state === "working"} />
          <span className="task-title">{t.title || untitled}</span>
          <time dateTime={t.updated_at} title={fullTime(t.updated_at)}>
            {ago(t.updated_at, now)}
          </time>
        </span>
        <span className="task-row-meta">
          <ChannelBadge name={t.channel} size="sm" />
          <span className={`tone-${tone} strong`}>{taskStateLabel[t.state]}</span>
          <span className="ellipsis">
            {chatKind(t)}
            {t.senders.length > 0 && ` · ${t.senders.slice(0, 2).join("、")}${t.senders.length > 2 ? ` 等 ${t.senders.length} 人` : ""}`}
          </span>
          {t.replies > 0 && (
            <span className="replies" title={`bot 回复了 ${t.replies} 次`}>
              <Icon name="reply" />
              {t.replies}
              <SrOnly> 次回复</SrOnly>
            </span>
          )}
        </span>
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
  const task = usePoll(() => api.task(project, id), POLL_SLOW, `task-${project}-${id}`);
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
          <h2>{t.title || untitled}</h2>
          <span className="conv-actions">
            {t.link && (
              <a className="btn btn-ghost btn-sm" href={t.link} target="_blank" rel="noreferrer noopener">
                在 GitHub 打开
                <Icon name="external" />
              </a>
            )}
            <CopyButton text={t.id} icon label="复制任务 id" />
          </span>
        </div>
        <dl className="pass">
          <div>
            <dt>来源</dt>
            <dd>
              <ChannelBadge name={t.channel} size="sm" />
              {onIssues(t) ? channelLabel(t.channel) : `${channelLabel(t.channel)} ${chatKind(t)}`}
            </dd>
          </div>
          <div>
            <dt>状态</dt>
            <dd className={`tone-${taskTone[t.state]}`}>
              <Mark tone={taskTone[t.state]} live={t.state === "working"} />
              {taskStateLabel[t.state]}
            </dd>
          </div>
          <div>
            <dt>参与者</dt>
            <dd className="ellipsis">{t.senders.join("、") || "—"}</dd>
          </div>
          <div>
            <dt>bot 回复</dt>
            <dd>{t.replies} 次</dd>
          </div>
          <div>
            <dt>创建于</dt>
            <dd title={fullTime(t.created_at)}>{ago(t.created_at, now)}</dd>
          </div>
        </dl>
        <p className="conv-id mono" title="会话 id">
          {t.chat_id}
        </p>
      </header>

      <ol className="timeline">
        {ts.map((turn, i) => (
          <li key={turn.index}>
            {i === firstNew && (
              <div className="new-divider">
                <span>以下 {t.unhandled} 条 bot 还没处理</span>
              </div>
            )}
            <Message turn={turn} now={now} />
          </li>
        ))}
      </ol>
    </div>
  );
}

function Message({ turn, now }: { turn: Turn; now: number }) {
  const first = turn.entries[0];

  if (turn.kind === "bot") {
    return (
      <article className="entry entry-bot">
        <header className="entry-head">
          <span className="role">bot</span>
          <time dateTime={first.at} title={fullTime(first.at)}>
            {ago(first.at, now)}
          </time>
        </header>
        <div className="entry-body">
          <Markdown text={first.text ?? ""} />
        </div>
      </article>
    );
  }

  const who = first.sender?.name || first.sender?.id || "用户";
  const text = turn.entries.filter((e) => !e.file).map((e) => e.text ?? "").join("");
  const files = turn.entries.filter(isFile);

  return (
    <article className="entry">
      <header className="entry-head">
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
      </header>
      <div className="entry-body">
        {text && <div className="plain">{text}</div>}
        {files.map((f) => (
          <div key={f.file} className="file" title={f.file}>
            <Icon name="paperclip" />
            <span className="file-name mono">{f.file.split("/").pop()}</span>
            <CopyButton text={f.file} icon label="复制路径" />
          </div>
        ))}
      </div>
    </article>
  );
}
