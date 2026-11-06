import { useState } from "react";
import type { Problem } from "./diagnose";
import { Icon } from "./icons";
import { ChannelBadge, Command, CopyButton, useFlag } from "./ui";

/** Something that needs the user: what happened, the main fix, and the steps behind it. */
export function ProblemCard({ problem: p, open: initiallyOpen = false, showProject = true }: { problem: Problem; open?: boolean; showProject?: boolean }) {
  const [open, setOpen] = useState(initiallyOpen);
  const stepsId = `steps-${p.key}`;

  return (
    <article className={`problem problem-${p.tone}${open ? " open" : ""}`}>
      <div className="problem-main">
        <span className="problem-icon" aria-hidden>
          {p.channel ? <ChannelBadge name={p.channel} size="md" /> : <Icon name={p.tone === "info" ? "plug" : "alert"} />}
        </span>
        <div className="problem-text">
          <h3>{showProject ? p.title : (p.local ?? p.title)}</h3>
          <p>{p.summary}</p>
        </div>
        <div className="problem-actions">
          <button type="button" className="btn btn-ghost" aria-expanded={open} aria-controls={stepsId} onClick={() => setOpen((o) => !o)}>
            {open ? "收起" : "怎么修"}
          </button>
          <Action problem={p} />
        </div>
      </div>

      {open && (
        <div className="problem-steps" id={stepsId}>
          <ol className="steps">
            {p.steps.map((s, i) => (
              <li key={i}>
                <span className="step-n" aria-hidden>
                  {i + 1}
                </span>
                <div>
                  <p className="step-title">{s.title}</p>
                  {s.detail && <p className="step-detail">{s.detail}</p>}
                  {s.command && <Command>{s.command}</Command>}
                  {s.link && !(p.action && "href" in p.action && p.action.href === s.link.href) && (
                    <a className="step-link" href={s.link.href} {...(s.link.href.startsWith("http") ? { target: "_blank", rel: "noreferrer noopener" } : {})}>
                      {s.link.label}
                      <Icon name={s.link.href.startsWith("http") ? "external" : "arrow"} />
                    </a>
                  )}
                </div>
              </li>
            ))}
          </ol>
          {p.raw && (
            <div className="raw">
              <div className="raw-head">
                <span>平台返回的原始错误</span>
                <CopyButton text={p.raw} icon label="复制错误" />
              </div>
              <code>{p.raw}</code>
            </div>
          )}
        </div>
      )}
    </article>
  );
}

function Action({ problem: p }: { problem: Problem }) {
  const [done, flag] = useFlag();
  const a = p.action;
  if (!a) return null;

  if ("command" in a) {
    return (
      <button
        type="button"
        className="btn btn-primary"
        onClick={() =>
          navigator.clipboard.writeText(a.command).then(flag)
        }
      >
        <Icon name={done ? "check" : "copy"} />
        {done ? "已复制" : a.label}
      </button>
    );
  }

  return (
    <a className="btn btn-primary" href={a.href} {...(a.external ? { target: "_blank", rel: "noreferrer noopener" } : {})}>
      {a.label}
      <Icon name={a.external ? "external" : "arrow"} />
    </a>
  );
}
