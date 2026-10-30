import { useEffect } from "react";
import { api, AuthError, hasToken, OfflineError } from "./api";
import { projectStateLabel, shortPath, uptime } from "./format";
import { useNow, usePoll } from "./hooks";
import { ProjectView } from "./ProjectView";
import { go, href, useRoute } from "./route";
import { SettingsView } from "./SettingsView";
import type { Overview, ProjectStatus } from "./types";
import { Banner, Command, Dot, Empty, Toasts } from "./ui";

export function projectTone(p: ProjectStatus): "ok" | "warn" | "bad" | "off" {
  switch (p.state) {
    case "running":
      return p.stopped?.length ? "warn" : "ok";
    case "starting":
    case "busy":
      return "warn";
    case "failed":
      return "bad";
    default:
      return "off";
  }
}

export function App() {
  const route = useRoute();
  const overview = usePoll(api.overview, 3000, "overview");
  const data = overview.data;

  // With nothing chosen, open the first project.
  useEffect(() => {
    if (route.page === "home" && data?.projects.length) go({ page: "project", project: data.projects[0].id, tab: "tasks" });
  }, [route.page, data]);

  if (!hasToken() || overview.error instanceof AuthError) return <AuthScreen />;
  if (!data && overview.error instanceof OfflineError) return <OfflineScreen />;
  if (!data) return <div className="boot" aria-busy="true" />;

  const project = route.page === "project" ? data.projects.find((p) => p.id === route.project) : undefined;

  return (
    <div className="shell">
      <Sidebar overview={data} active={route.page === "project" ? route.project : route.page} offline={overview.error instanceof OfflineError} />
      <main className="main">
        {overview.error instanceof OfflineError && <Banner tone="bad">和 gateway 断开了，正在重连…下面是断开前的数据。</Banner>}
        {!data.jev && (
          <Banner tone="warn">
            还没有 TypeSafe API key：每条消息都要先经过 Jev，没有 key 时 gateway 不会连接任何项目。<a href={href({ page: "settings" })}>去设置</a>
          </Banner>
        )}
        {route.page === "settings" && <SettingsView overview={data} />}
        {route.page === "project" && project && (
          <ProjectView key={project.id} project={project} overview={data} tab={route.tab} task={route.task} onChanged={overview.reload} />
        )}
        {route.page === "project" && !project && (
          <Empty title="没有这个项目">
            <p>它可能已经从 gateway 里移除了。</p>
          </Empty>
        )}
        {route.page === "home" && !data.projects.length && <NoProjects />}
      </main>
      <Toasts />
    </div>
  );
}

function Sidebar({ overview, active, offline }: { overview: Overview; active: number | string; offline: boolean }) {
  const now = useNow();

  return (
    <nav className="sidebar" aria-label="项目">
      <div className="brand">
        <div className="brand-mark" aria-hidden />
        <div>
          <div className="brand-name">claude-bot gateway</div>
          <div className="brand-meta">
            <Dot tone={offline ? "bad" : "ok"} pulse={!offline} />
            {offline ? "已断开" : `运行中 · ${overview.version ?? overview.self}`}
            {!offline && overview.started_at && <span title={`pid ${overview.pid}`}> · 已运行 {uptime(overview.started_at, now)}</span>}
          </div>
        </div>
      </div>

      <div className="nav-label">
        项目 <span className="count">{overview.projects.length}</span>
      </div>
      <ul className="nav-list">
        {overview.projects.map((p) => (
          <li key={p.id}>
            <a className={`nav-item${active === p.id ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "tasks" })} aria-current={active === p.id ? "page" : undefined}>
              <Dot tone={projectTone(p)} pulse={p.state === "starting"} />
              <span className="nav-text">
                <span className="nav-name">{p.name}</span>
                <span className="nav-sub" title={p.dir}>
                  {p.state === "running" && !p.stopped?.length ? shortPath(p.dir) : projectStateLabel[p.state]}
                </span>
              </span>
              {p.pending > 0 && (
                <span className="pill pill-accent" title={`${p.pending} 件待处理`}>
                  {p.pending}
                </span>
              )}
            </a>
          </li>
        ))}
        {!overview.projects.length && <li className="nav-empty">还没有项目</li>}
      </ul>

      <div className="nav-foot">
        <a className={`nav-item${active === "settings" ? " active" : ""}`} href={href({ page: "settings" })}>
          <span className="gear" aria-hidden>
            ⚙
          </span>
          <span className="nav-text">
            <span className="nav-name">共用设置</span>
            <span className="nav-sub">TypeSafe key · GitHub token</span>
          </span>
          {(!overview.jev || !overview.github_token) && <Dot tone={overview.jev ? "off" : "warn"} />}
        </a>
      </div>
    </nav>
  );
}

function NoProjects() {
  return (
    <div className="center-page">
      <Empty title="还没有项目" icon={<span className="big-icon">⌁</span>}>
        <p>在要值守的仓库里运行 init，项目会自动登记到 gateway，几秒内出现在这里：</p>
        <Command>cd 你的项目 && claude-bot init</Command>
        <p className="muted">以前用 claude-bot start 单独跑的项目，在仓库里运行：</p>
        <Command>claude-bot gateway import</Command>
      </Empty>
    </div>
  );
}

function AuthScreen() {
  return (
    <div className="center-page full">
      <Empty title="需要访问令牌" icon={<span className="big-icon">⚿</span>}>
        <p>这个页面能看到各项目的对话、改渠道凭证，所以要带着令牌打开。在终端里运行：</p>
        <Command>claude-bot gateway open</Command>
        <p className="muted">它会用带令牌的地址打开这个页面；令牌存在这个浏览器里，之后直接打开也行。</p>
      </Empty>
    </div>
  );
}

function OfflineScreen() {
  return (
    <div className="center-page full">
      <Empty title="连不上 gateway" icon={<span className="big-icon">⏻</span>}>
        <p>gateway 可能没在运行。在终端里启动它：</p>
        <Command>claude-bot gateway start</Command>
        <p className="muted">
          <Dot tone="warn" pulse /> 正在每隔几秒重试，连上后自动恢复。
        </p>
      </Empty>
    </div>
  );
}
