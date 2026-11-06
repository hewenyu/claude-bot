import { useEffect, useState } from "react";
import { api, AuthError, hasToken, OfflineError } from "./api";
import { allProblems } from "./diagnose";
import { paletteKey, uptime } from "./format";
import { Home } from "./Home";
import { POLL_FAST, useNow, usePoll } from "./hooks";
import { Icon } from "./icons";
import { Palette } from "./Palette";
import { ProjectView } from "./ProjectView";
import { href, useRoute, type Route } from "./route";
import { SettingsView } from "./SettingsView";
import { bySeverity, presence, projectLabel, projectTone } from "./status";
import type { Overview } from "./types";
import { AppIcon, Banner, Command, Empty, Mark, Orb, SrOnly, Toasts } from "./ui";

export function App() {
  const route = useRoute();
  const overview = usePoll(api.overview, POLL_FAST, "overview");
  const data = overview.data;
  const offline = overview.error instanceof OfflineError;
  const [palette, setPalette] = useState(false);
  const [drawer, setDrawer] = useState(false);

  // A background tab says when something is broken.
  const broken = data?.projects.filter((p) => projectTone(p) === "bad").length ?? 0;
  useEffect(() => {
    document.title = offline ? "已断开 · claude-bot gateway" : broken ? `${broken} 个项目出错 · claude-bot gateway` : "claude-bot gateway";
  }, [broken, offline]);

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (!((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k")) return;
      e.preventDefault();
      // A dialog keeps the keyboard: the palette would open over it and take its focus.
      if (document.querySelector('.modal[aria-modal="true"]')) return;
      setPalette((x) => !x);
    };
    window.addEventListener("keydown", on);

    return () => window.removeEventListener("keydown", on);
  }, []);

  // Going somewhere closes the drawer.
  useEffect(() => setDrawer(false), [route]);

  if (!hasToken() || overview.error instanceof AuthError) return <AuthScreen />;
  if (!data && offline) return <OfflineScreen />;
  if (!data) return <div className="boot" aria-busy="true" />;

  const project = route.page === "project" ? data.projects.find((p) => p.id === route.project) : undefined;

  return (
    <div className={`shell${drawer ? " drawer-open" : ""}`}>
      <Rail overview={data} route={route} offline={offline} onSearch={() => setPalette(true)} onClose={() => setDrawer(false)} />
      {drawer && <div className="drawer-backdrop" onClick={() => setDrawer(false)} />}

      <div className="main">
        <header className="topbar">
          <button type="button" className="icon-btn" onClick={() => setDrawer(true)} aria-label="打开项目列表">
            <Icon name="menu" />
          </button>
          <a className="topbar-brand" href={href({ page: "home" })}>
            <AppIcon working={presence(data, offline) === "working"} size="sm" />
            claude-bot
          </a>
          <button type="button" className="icon-btn" onClick={() => setPalette(true)} aria-label="跳转">
            <Icon name="search" />
          </button>
        </header>

        {offline && (
          <div className="page-banner">
            <Banner tone="bad">和 gateway 断开了，正在重连…下面是断开前的数据。</Banner>
          </div>
        )}

        {route.page === "home" && <Home overview={data} offline={offline} onSearch={() => setPalette(true)} />}
        {route.page === "settings" && <SettingsView overview={data} onChanged={overview.reload} />}
        {route.page === "project" && project && (
          <ProjectView key={project.id} project={project} overview={data} tab={route.tab} task={route.task} onChanged={overview.reload} />
        )}
        {route.page === "project" && !project && (
          <div className="page page-narrow">
            <Empty title="没有这个项目" icon={<Icon name="folder" />}>
              <p>它可能已经从 gateway 里移除了。</p>
              <a className="btn" href={href({ page: "home" })}>
                回到总览
              </a>
            </Empty>
          </div>
        )}
      </div>

      {palette && <Palette overview={data} onClose={() => setPalette(false)} />}
      <Toasts />
    </div>
  );
}

function Rail({ overview, route, offline, onSearch, onClose }: { overview: Overview; route: Route; offline: boolean; onSearch: () => void; onClose: () => void }) {
  const now = useNow();
  const problems = allProblems(overview).length;
  const active = route.page === "project" ? route.project : route.page;

  return (
    <nav className="rail" aria-label="导航">
      <div className="rail-top">
        <a className="brand" href={href({ page: "home" })}>
          <AppIcon working={presence(overview, offline) === "working"} />
          <span>claude-bot</span>
        </a>
        <button type="button" className="icon-btn rail-close" onClick={onClose} aria-label="关闭项目列表">
          <Icon name="cross" />
        </button>
      </div>

      <button type="button" className="rail-search" onClick={onSearch}>
        <Icon name="search" />
        <span>跳转</span>
        <kbd>{paletteKey}</kbd>
      </button>

      <a className={`rail-item${active === "home" ? " active" : ""}`} href={href({ page: "home" })} aria-current={active === "home" ? "page" : undefined}>
        <Icon name="home" />
        <span className="rail-name">总览</span>
        {problems > 0 && (
          <span className="count count-bad" title={`${problems} 件事需要你处理`}>
            {problems}
            <SrOnly> 件事需要你处理</SrOnly>
          </span>
        )}
      </a>

      <h2 className="rail-label">项目</h2>
      <ul className="rail-list">
        {bySeverity(overview.projects).map((p) => {
          const tone = projectTone(p);
          const on = active === p.id;

          return (
            <li key={p.id}>
              <a className={`rail-item${on ? " active" : ""}`} href={href({ page: "project", project: p.id, tab: "tasks" })} aria-current={on ? "page" : undefined}>
                <Mark tone={tone} />
                <span className="rail-text">
                  <span className="rail-name">{p.name}</span>
                  {tone !== "ok" && <span className={`rail-sub tone-${tone}`}>{projectLabel(p)}</span>}
                  {tone === "ok" && <SrOnly>，运行中</SrOnly>}
                </span>
                {p.pending > 0 && (
                  <span className="count" title={`${p.pending} 件进行中`}>
                    {p.pending}
                    <SrOnly> 件进行中</SrOnly>
                  </span>
                )}
              </a>
            </li>
          );
        })}
        {!overview.projects.length && <li className="rail-empty">还没有项目</li>}
      </ul>

      <div className="rail-foot">
        <a className={`rail-item${active === "settings" ? " active" : ""}`} href={href({ page: "settings" })} aria-current={active === "settings" ? "page" : undefined}>
          <Icon name="gear" />
          <span className="rail-name">共用设置</span>
          {!overview.jev && <Mark tone="bad" />}
        </a>
        <p className="rail-status" title={overview.pid ? `pid ${overview.pid}` : undefined}>
          <Mark tone={offline ? "bad" : "ok"} />
          {offline ? "gateway 已断开" : `gateway 运行中${overview.started_at ? ` · ${uptime(overview.started_at, now)}` : ""}`}
          <span className="rail-version">{overview.version ?? overview.self}</span>
        </p>
      </div>
    </nav>
  );
}

function AuthScreen() {
  return (
    <div className="solo">
      <Orb state="off" size="lg" />
      <h1>需要访问令牌</h1>
      <p>这个页面能看到各项目的对话、改渠道凭证，所以要带着令牌打开。在终端里运行：</p>
      <Command>claude-bot gateway open</Command>
      <p className="muted">它会用带令牌的地址打开这个页面；令牌存在这个浏览器里，之后直接打开也行。</p>
    </div>
  );
}

function OfflineScreen() {
  return (
    <div className="solo">
      <Orb state="off" size="lg" />
      <h1>连不上 gateway</h1>
      <p>gateway 可能没在运行。在终端里启动它：</p>
      <Command>claude-bot gateway start</Command>
      <p className="muted retry">
        <Mark tone="warn" live />
        正在每隔几秒重试，连上后自动恢复。
      </p>
    </div>
  );
}
