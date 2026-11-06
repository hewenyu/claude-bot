// A project's agent: whether the gateway runs /bot there by itself, how it
// is doing, and its terminal.

import { lazy, Suspense, useState } from "react";
import { api, ApiError } from "./api";
import { ago, fullTime } from "./format";
import { useNow, usePoll } from "./hooks";
import { Icon } from "./icons";
import { runLabel, runTone } from "./status";
import type { ProjectStatus, Run } from "./types";
import { Banner, Mark, errorText, toast } from "./ui";

// xterm is most of the page's weight: it loads with the first terminal.
const Terminal = lazy(() => import("./Terminal").then((m) => ({ default: m.Terminal })));

const liveAgent = (r: Run) => !!r.agent && ["starting", "working", "blocked", "idle"].includes(r.agent.status);

export function AgentPanel({ project }: { project: ProjectStatus }) {
  const run = usePoll(() => api.run(project.id), 3000, `run-${project.id}`);
  const [busy, setBusy] = useState(false);

  if (run.error instanceof ApiError && run.error.status === 503) {
    return <Banner tone="warn">gateway 现在不运行 agent：它的页面没能启动时也不跑 agent，看一下 gateway 的日志。</Banner>;
  }
  if (run.error && !run.data) return <Banner tone="bad">读不到这个项目的 agent：{errorText(run.error)}</Banner>;
  if (!run.data) return <div className="loading" aria-busy="true" />;

  const r = run.data;
  const on = r.state !== "off";

  const act = (what: string, f: () => Promise<unknown>) => {
    setBusy(true);
    f()
      .then(() => toast(what), (e) => toast(errorText(e), "bad"))
      .then(run.reload)
      .finally(() => setBusy(false));
  };

  return (
    <div className="agent">
      <section className="card agent-card">
        <header className="card-head">
          <div className="card-title">
            <h2>自动处理</h2>
            <span className="muted">有待办时，gateway 在这个项目里启动 Claude Code 运行 /bot，怎么处理由项目的 bot.md 决定。</span>
          </div>
          <span className="grow" />
          <label className="switch">
            <input type="checkbox" role="switch" checked={on} disabled={busy} onChange={(e) => act(e.target.checked ? "已打开自动处理" : "已关闭自动处理", () => api.setRunner(project.id, e.target.checked))} />
            <span className="switch-track" aria-hidden />
            <span>{on ? "已打开" : "已关闭"}</span>
          </label>
        </header>

        {on ? <RunState run={r} busy={busy} act={act} /> : <p className="muted small">打开后，有待办时 gateway 在这个项目里启动 Claude Code 跑 <code>/bot</code>，一批处理完就关掉；打开时别再手动跑，不然两个 Claude 会抢同一批任务。</p>}
      </section>

      {r.agent_id ? (
        <div className="term-frame">
          <Suspense fallback={null}>
            <Terminal agent={r.agent_id} live={liveAgent(r)} />
          </Suspense>
        </div>
      ) : (
        on && <p className="muted agent-none">还没有 agent，有待办时会启动。</p>
      )}

      {!!r.log?.length && <RunLog run={r} />}
    </div>
  );
}

function RunState({ run: r, busy, act }: { run: Run; busy: boolean; act: (what: string, f: () => Promise<unknown>) => void }) {
  const now = useNow();
  const tone = runTone(r.state);
  const agent = r.agent_id;
  const live = liveAgent(r);
  const canResume = ["waiting", "failed", "resting"].includes(r.state);
  const working = r.agent?.status === "working" || r.agent?.status === "blocked";

  return (
    <>
      <div className="run-state">
        <span className={`state-text tone-${tone}`}>
          <Mark tone={tone} live={r.state === "running"} />
          {runLabel[r.state]}
        </span>
        {r.detail && <span className="muted">{r.detail}</span>}
        <span className="grow" />
        <span className="muted small" title={fullTime(r.updated_at)}>
          {r.pending} 件待办 · {ago(r.updated_at, now)}
        </span>
      </div>

      {r.error && r.state === "failed" && (
        <p className="result result-bad">
          <Icon name="cross" />
          {r.error}
        </p>
      )}
      {r.summary && (
        <blockquote className="run-summary" title="上一轮结束时 Claude 说的">
          {r.summary}
        </blockquote>
      )}

      <div className="run-actions">
        {canResume && (
          <button type="button" className="btn btn-sm" disabled={busy} onClick={() => act("已让它再跑一轮", () => api.resumeRunner(r.project))}>
            再跑一轮
          </button>
        )}
        {agent && working && (
          <button type="button" className="btn btn-sm" disabled={busy} onClick={() => act("已中断", () => api.agentAction(agent, "interrupt"))}>
            中断
          </button>
        )}
        {agent && live && (
          <button type="button" className="btn btn-sm btn-ghost btn-danger-text" disabled={busy} onClick={() => act("已结束 agent", () => api.agentAction(agent, "kill"))}>
            结束 agent
          </button>
        )}
      </div>
    </>
  );
}

function RunLog({ run }: { run: Run }) {
  const lines = [...(run.log ?? [])].reverse();

  return (
    <details className="run-log">
      <summary>记录</summary>
      <ol>
        {lines.map((l, i) => (
          <li key={i}>
            <time className="muted mono" dateTime={l.at}>
              {fullTime(l.at)}
            </time>
            <span>{l.text}</span>
          </li>
        ))}
      </ol>
    </details>
  );
}
