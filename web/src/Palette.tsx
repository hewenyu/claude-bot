import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { api } from "./api";
import { projectLabel, projectTone } from "./status";
import { taskStateLabel } from "./format";
import { Icon, type IconName } from "./icons";
import { go, type Route } from "./route";
import type { Overview, TaskSummary } from "./types";
import { ChannelBadge, Mark, type Tone } from "./ui";

interface Item {
  key: string;
  group: string;
  title: string;
  sub?: string;
  route: Route;
  mark?: Tone;
  icon?: IconName;
  channel?: string;
  text: string; // what the query matches against
}

const taskTone = { pending: "warn", working: "info", replied: "ok", closed: "off" } as const;

/** ⌘K: jump to a project, a task or a page by typing part of its name. */
export function Palette({ overview, onClose }: { overview: Overview; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);
  const [tasks, setTasks] = useState<{ project: number; name: string; task: TaskSummary }[]>([]);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);

  useEffect(() => {
    input.current?.focus();
    let live = true;
    Promise.all(
      overview.projects.map((p) =>
        api.tasks(p.id).then(
          (ts) => ts.map((task) => ({ project: p.id, name: p.name, task })),
          () => [],
        ),
      ),
    ).then((all) => live && setTasks(all.flat()));

    return () => {
      live = false;
    };
  }, [overview.projects]);

  const items = useMemo(() => {
    const out: Item[] = [
      { key: "home", group: "页面", title: "总览", sub: "现在需要你处理的事", route: { page: "home" }, icon: "home", text: "总览 home overview" },
      { key: "settings", group: "页面", title: "共用设置", sub: "TypeSafe key · GitHub token", route: { page: "settings" }, icon: "gear", text: "共用设置 settings typesafe github token key" },
    ];

    for (const p of overview.projects) {
      out.push({ key: `p${p.id}`, group: "项目", title: p.name, sub: projectLabel(p), route: { page: "project", project: p.id, tab: "tasks" }, mark: projectTone(p), text: `${p.name} ${p.dir}` });
      out.push({ key: `c${p.id}`, group: "项目", title: `${p.name} · 渠道`, sub: "凭证、测试连接、添加渠道", route: { page: "project", project: p.id, tab: "channels" }, icon: "plug", text: `${p.name} 渠道 channels` });
    }

    const open = (s: string) => s === "pending" || s === "working";
    const sorted = [...tasks].sort((a, b) => Number(open(b.task.state)) - Number(open(a.task.state)) || b.task.updated_at.localeCompare(a.task.updated_at));
    for (const { project, name, task: t } of sorted) {
      out.push({
        key: `t${project}-${t.id}`,
        group: "任务",
        title: t.title || "（没有文字）",
        sub: `${name} · ${taskStateLabel[t.state]}${t.senders.length ? ` · ${t.senders.join("、")}` : ""}`,
        route: { page: "project", project, tab: "tasks", task: t.id },
        mark: taskTone[t.state],
        channel: t.channel,
        text: `${t.title} ${t.senders.join(" ")} ${name} ${t.id}`,
      });
    }

    const q = query.trim().toLowerCase();
    if (!q) return out.filter((it) => it.group !== "任务" || it.mark === "warn" || it.mark === "info").slice(0, 30);

    return out.filter((it) => it.text.toLowerCase().includes(q) || it.title.toLowerCase().includes(q)).slice(0, 40);
  }, [overview.projects, tasks, query]);

  useEffect(() => setIndex(0), [query]);
  useEffect(() => {
    list.current?.querySelector(`[data-i="${index}"]`)?.scrollIntoView({ block: "nearest" });
  }, [index]);

  const pick = (it: Item | undefined) => {
    if (!it) return;
    onClose();
    go(it.route);
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setIndex((i) => Math.min(i + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      pick(items[index]);
    } else if (e.key === "Escape") {
      onClose();
    }
  };

  let lastGroup = "";

  return (
    <div className="palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="跳转">
        <div className="palette-input">
          <Icon name="search" />
          <input
            ref={input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKey}
            placeholder="跳到项目、任务或页面…"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={items[index] ? `pi-${items[index].key}` : undefined}
            autoComplete="off"
            spellCheck={false}
          />
          <kbd>esc</kbd>
        </div>
        <ul className="palette-list" id="palette-list" role="listbox" ref={list}>
          {items.map((it, i) => {
            const head = it.group !== lastGroup;
            lastGroup = it.group;

            return (
              <li key={it.key} role="presentation">
                {head && <div className="palette-group">{it.group}</div>}
                <div
                  id={`pi-${it.key}`}
                  data-i={i}
                  role="option"
                  aria-selected={i === index}
                  className={`palette-item${i === index ? " on" : ""}`}
                  onMouseMove={() => setIndex(i)}
                  onClick={() => pick(it)}
                >
                  <span className="palette-lead">
                    {it.channel ? <ChannelBadge name={it.channel} size="sm" /> : it.mark ? <Mark tone={it.mark} live={it.mark === "info"} /> : <Icon name={it.icon ?? "chevron"} />}
                  </span>
                  <span className="palette-title">{it.title}</span>
                  {it.sub && <span className="palette-sub">{it.sub}</span>}
                  {i === index && <Icon name="arrow" className="palette-go" />}
                </div>
              </li>
            );
          })}
          {!items.length && <li className="palette-none">没有找到「{query.trim()}」</li>}
        </ul>
      </div>
    </div>
  );
}
