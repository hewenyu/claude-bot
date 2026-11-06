import { useEffect, useRef } from "react";
import { tasksOf } from "./api";
import { allProblems } from "./diagnose";
import { ago, channel, fullTime, paletteKey, taskStateLabel } from "./format";
import { POLL_SLOW, useNow, usePoll } from "./hooks";
import { Icon } from "./icons";
import { ProblemCard } from "./Problems";
import { href } from "./route";
import { bySeverity, isOpenTask, presence, projectLabel, projectTone, taskTone, untitled } from "./status";
import type { Overview, ProjectStatus } from "./types";
import { ChannelBadge, Command, Mark, Orb, SrOnly } from "./ui";

/** Open tasks across the projects that have any, newest first. */
function useOpenTasks(projects: ProjectStatus[]) {
  const busy = projects.filter((p) => p.pending > 0);
  const counts = busy.map((p) => `${p.id}:${p.pending}`).join(",");

  // One fixed key, so a changed count refetches without dropping the list
  // shown meanwhile (a new key would start over from "loading").
  const open = usePoll(
    () => tasksOf(busy).then((all) => all.filter((x) => isOpenTask(x.task.state)).sort((a, b) => b.task.updated_at.localeCompare(a.task.updated_at))),
    POLL_SLOW,
    "open-tasks",
  );
  const { reload } = open;
  const seen = useRef(counts);
  useEffect(() => {
    if (seen.current === counts) return;
    seen.current = counts;
    reload();
  }, [counts, reload]);

  return open;
}

export function Home({ overview, onSearch }: { overview: Overview; onSearch: () => void }) {
  const problems = allProblems(overview);
  const open = useOpenTasks(overview.projects);
  const now = useNow();
  const troubled = new Set(problems.map((p) => p.project?.id).filter((x) => x !== undefined));
  const healthy = bySeverity(overview.projects).filter((p) => !troubled.has(p.id));
  const working = open.data?.filter((x) => x.task.state === "working").length ?? 0;
  const waiting = open.data?.filter((x) => x.task.state === "pending").length ?? 0;

  if (!overview.projects.length) return <Welcome overview={overview} />;

  const headline = problems.length ? `${problems.length} 件事需要你处理` : "一切正常";
  const bits = [
    problems.length ? `${healthy.length} 个项目正常` : `${overview.projects.length} 个项目都在线`,
    working ? `bot 正在处理 ${working} 件事` : open.data && !waiting ? "bot 现在没有要处理的事" : "",
    waiting ? `${waiting} 件在等 bot 接手` : "",
  ].filter(Boolean);

  return (
    <div className="page page-narrow home">
      <header className="brief">
        <Orb state={presence(overview, false)} size="lg" />
        <div>
          <h1>{headline}</h1>
          <p>{bits.join("，")}。</p>
        </div>
      </header>

      {problems.length > 0 && (
        <section className="section section-first" aria-label="需要你处理">
          <div className="stack">
            {problems.map((p, i) => (
              <ProblemCard key={p.key} problem={p} open={i === 0} />
            ))}
          </div>
        </section>
      )}

      <section className="section" aria-labelledby="h-open">
        <h2 id="h-open">正在进行</h2>
        {open.data && open.data.length > 0 ? (
          <ul className="rows">
            {open.data.map(({ project, task: t }) => (
              <li key={`${project.id}-${t.id}`}>
                <a className="row" href={href({ page: "project", project: project.id, tab: "tasks", task: t.id })}>
                  <ChannelBadge name={t.channel} size="sm" />
                  <span className="row-main">
                    <span className="row-title">{t.title || untitled}</span>
                    <span className="row-sub">
                      {project.name} · {t.senders.slice(0, 2).join("、") || channel(t.channel).label}
                    </span>
                  </span>
                  <span className={`state-text tone-${taskTone[t.state]}`}>
                    <Mark tone={taskTone[t.state]} live={t.state === "working"} />
                    {taskStateLabel[t.state]}
                  </span>
                  <time className="row-time" dateTime={t.updated_at} title={fullTime(t.updated_at)}>
                    {ago(t.updated_at, now)}
                  </time>
                </a>
              </li>
            ))}
          </ul>
        ) : (
          <p className="quiet">{open.data || !overview.projects.some((p) => p.pending) ? "没有进行中的事。有人发消息、@ bot 或开 issue 时，会出现在这里。" : "正在读取…"}</p>
        )}
      </section>

      {healthy.length > 0 && (
        <p className="fine">
          <span className="fine-lead">{problems.length ? `其余 ${healthy.length} 个项目正常：` : "项目："}</span>
          {healthy.map((p) => (
            <a key={p.id} className="fine-item" href={href({ page: "project", project: p.id, tab: "tasks" })} title={projectLabel(p)}>
              <Mark tone={projectTone(p)} live={p.state === "starting"} />
              {p.name}
              <SrOnly>，{projectLabel(p)}</SrOnly>
            </a>
          ))}
        </p>
      )}

      <div className="dock-wrap">
        <button type="button" className="dock" onClick={onSearch}>
          <Icon name="search" />
          <span>跳到项目、任务或设置…</span>
          <kbd>{paletteKey}</kbd>
        </button>
      </div>
    </div>
  );
}

function Welcome({ overview }: { overview: Overview }) {
  return (
    <div className="page page-narrow home">
      <header className="brief">
        <Orb state="calm" size="lg" />
        <div>
          <h1>把第一个项目接进来</h1>
          <p>gateway 已经在运行。登记一个仓库，在群里 @ 一下，Claude Code 就去仓库里把活干了。</p>
        </div>
      </header>
      <ol className="steps steps-big">
        {!overview.jev && (
          <li>
            <span className="step-n">1</span>
            <div>
              <p className="step-title">填上 TypeSafe API key</p>
              <p className="step-detail">每条消息先经过 Jev 筛选。没有 key，gateway 不会连接任何项目。</p>
              <a className="step-link" href={href({ page: "settings" })}>
                去「共用设置」
                <Icon name="arrow" />
              </a>
            </div>
          </li>
        )}
        <li>
          <span className="step-n">{overview.jev ? 1 : 2}</span>
          <div>
            <p className="step-title">在要值守的仓库里运行 init</p>
            <p className="step-detail">它会让你选渠道、填凭证，并把项目登记到 gateway，几秒内出现在这里。</p>
            <Command>cd 你的项目 && claude-bot init</Command>
          </div>
        </li>
        <li>
          <span className="step-n">{overview.jev ? 2 : 3}</span>
          <div>
            <p className="step-title">打开项目的「自动处理」</p>
            <p className="step-detail">有待办时 gateway 在仓库里启动 Claude Code 跑 /bot，处理完一批回复到原消息下面就关掉；也可以自己在仓库里的 Claude Code 运行一次：</p>
            <Command>/bot</Command>
          </div>
        </li>
      </ol>
      <p className="quiet">以前用 claude-bot start 单独跑的项目，在仓库里运行 claude-bot gateway import 接进来。</p>
    </div>
  );
}
