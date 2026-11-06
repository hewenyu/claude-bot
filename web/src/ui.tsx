import { useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from "react";
import { channelLabel } from "./channelKinds";
import { useAction } from "./hooks";
import { Icon } from "./icons";
import { ChannelLogo, hasLogo } from "./logos";

export type Tone = "ok" | "warn" | "bad" | "off" | "info";

/** The channel's official logo; a lettered tile for a channel without one. */
export function ChannelBadge({ name, size = "md" }: { name: string; size?: "sm" | "md" | "lg" }) {
  const label = channelLabel(name);

  if (hasLogo(name)) {
    return (
      <span className={`badge badge-${size} badge-logo`} title={label} aria-hidden>
        <ChannelLogo name={name} />
      </span>
    );
  }

  return (
    <span className={`badge badge-${size}`} title={label} aria-hidden>
      {name.slice(0, 2).toUpperCase()}
    </span>
  );
}

/**
 * A state mark: shape and colour both carry it, so it reads without colour.
 * ok is filled, warn a ring, bad struck through, off a hollow grey ring,
 * info (the bot at work) a small breathing orb.
 */
export function Mark({ tone, live }: { tone: Tone; live?: boolean }) {
  return <span className={`mark mark-${tone}${live ? " mark-live" : ""}`} aria-hidden />;
}

/** A state in words after its mark, in the state's colour. */
export function StateText({ tone, live, children }: { tone: Tone; live?: boolean; children: ReactNode }) {
  return (
    <span className={`state-text tone-${tone}`}>
      <Mark tone={tone} live={live} />
      {children}
    </span>
  );
}

/** How a request went, under the form or card that made it. */
export function Result({ tone, children }: { tone: "ok" | "bad"; children: ReactNode }) {
  return (
    <p className={`result result-${tone}`} role="status">
      <Icon name={tone === "ok" ? "check" : "cross"} />
      {children}
    </p>
  );
}

/** The gateway's presence: calm when all is well, breathing while a bot works, red when something is broken. */
export function Orb({ state = "calm", size = "md" }: { state?: "calm" | "working" | "bad" | "off"; size?: "sm" | "md" | "lg" }) {
  return <span className={`orb orb-${size} orb-${state}`} aria-hidden />;
}

/**
 * The app icon (docs/logo.svg): an ink tile, the terminal prompt and the orb
 * after it. Beside the name it is the brand, and its orb breathes while a bot works.
 */
export function AppIcon({ working = false, size = "md" }: { working?: boolean; size?: "sm" | "md" }) {
  const id = useId();

  return (
    <svg className={`app-icon app-icon-${size}${working ? " app-icon-working" : ""}`} viewBox="0 0 64 64" aria-hidden>
      <defs>
        <linearGradient id={`${id}t`} x2="0" y2="1">
          <stop className="app-icon-t0" offset="0" />
          <stop className="app-icon-t1" offset="1" />
        </linearGradient>
        <radialGradient id={`${id}o`} cy=".45" r=".5">
          <stop className="app-icon-o1" offset="0" />
          <stop className="app-icon-o2" offset=".52" />
          <stop className="app-icon-o3" offset="1" />
        </radialGradient>
      </defs>
      <rect x="2" y="2" width="60" height="60" rx="15" fill={`url(#${id}t)`} />
      <rect className="app-icon-edge" x="2.5" y="2.5" width="59" height="59" rx="14.5" fill="none" stroke="#fff" />
      <path d="M14.4 21 25.4 32 14.4 43" fill="none" stroke="#fff" strokeWidth="6.5" strokeLinecap="round" strokeLinejoin="round" />
      <circle className="app-icon-orb" cx="43.4" cy="32" r="9.5" fill={`url(#${id}o)`} />
    </svg>
  );
}

/** Text for screen readers only: what a mark or a tooltip says to the eye. */
export function SrOnly({ children }: { children: ReactNode }) {
  return <span className="sr-only">{children}</span>;
}

/** A flag that turns itself off again after ms, for "已复制"-style feedback. */
export function useFlag(ms = 1500): [boolean, () => void] {
  const [on, setOn] = useState(false);

  useEffect(() => {
    if (!on) return;
    const t = window.setTimeout(() => setOn(false), ms);

    return () => window.clearTimeout(t);
  }, [on, ms]);

  return [on, () => setOn(true)];
}

/**
 * Copies text to the clipboard and resolves whether it could; with msg, a
 * toast says it did. When the browser refuses, a toast says so.
 */
export function copyText(text: string, msg?: string): Promise<boolean> {
  return Promise.resolve()
    .then(() => navigator.clipboard.writeText(text))
    .then(
      () => {
        if (msg) toast(msg);

        return true;
      },
      () => {
        toast("没能复制：浏览器不让这个页面写剪贴板", "bad");

        return false;
      },
    );
}

/** Copies text; says so for a moment. With icon, a square icon button. */
export function CopyButton({ text, label = "复制", icon }: { text: string; label?: string; icon?: boolean }) {
  const [done, flag] = useFlag();
  const copy = () => copyText(text).then((ok) => ok && flag());

  if (icon) {
    return (
      <button type="button" className={`icon-btn${done ? " done" : ""}`} onClick={copy} aria-label={`${label}：${text}`} title={done ? "已复制" : label}>
        <Icon name={done ? "check" : "copy"} />
      </button>
    );
  }

  return (
    <button type="button" className="btn btn-ghost btn-sm" onClick={copy} aria-label={`${label}：${text}`}>
      <Icon name={done ? "check" : "copy"} />
      {done ? "已复制" : label}
    </button>
  );
}

/** A command to run in a terminal, with a copy button. */
export function Command({ children }: { children: string }) {
  return (
    <div className="command">
      <code>{children}</code>
      <CopyButton text={children} icon label="复制命令" />
    </div>
  );
}

export function Empty({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="empty">
      {icon && <div className="empty-icon">{icon}</div>}
      <p className="empty-title">{title}</p>
      {children && <div className="empty-body">{children}</div>}
    </div>
  );
}

export function Banner({ tone, children }: { tone: "warn" | "bad" | "info"; children: ReactNode }) {
  return (
    <div className={`banner banner-${tone}`} role={tone === "bad" ? "alert" : "status"}>
      <Icon name={tone === "info" ? "chat" : "alert"} />
      <div>{children}</div>
    </div>
  );
}

const focusable = 'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/**
 * Keeps Tab inside ref while mounted, closes on Escape, and gives focus back on
 * close. It runs once per mount: callers pass inline onClose arrows and the
 * page re-renders on every poll, so depending on onClose would pull focus back
 * to the first field every few seconds.
 */
export function useTrap(ref: RefObject<HTMLElement | null>, onClose: () => void) {
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const box = ref.current;
    (box?.querySelector<HTMLElement>(".modal-body :is(input, select, textarea)") ?? box?.querySelector<HTMLElement>(focusable))?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") return close.current();
      if (e.key !== "Tab" || !ref.current) return;

      const items = [...ref.current.querySelectorAll<HTMLElement>(focusable)];
      if (!items.length) return;

      const head = items[0];
      const tail = items[items.length - 1];
      const inside = ref.current.contains(document.activeElement);

      if (e.shiftKey && (document.activeElement === head || !inside)) {
        e.preventDefault();
        tail.focus();
      } else if (!e.shiftKey && (document.activeElement === tail || !inside)) {
        e.preventDefault();
        head.focus();
      }
    };
    document.addEventListener("keydown", onKey);

    return () => {
      document.removeEventListener("keydown", onKey);
      prev?.focus();
    };
  }, [ref]);
}

/** A dialog; Escape or the backdrop closes it, and Tab stays inside it. */
export function Modal({ title, onClose, children, footer, wide }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const titleId = useId();
  useTrap(ref, onClose);

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`modal${wide ? " modal-wide" : ""}`} role="dialog" aria-modal="true" aria-labelledby={titleId} ref={ref}>
        <div className="modal-head">
          <h2 id={titleId}>{title}</h2>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="关闭">
            <Icon name="cross" />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  );
}

/**
 * Asks before doing something that cannot be undone. onConfirm does it; a
 * failure shows under the text and leaves the dialog open.
 */
export function ConfirmModal({
  title,
  confirm,
  busyLabel,
  disabled,
  onConfirm,
  onDone,
  onClose,
  children,
}: {
  title: string;
  /** The danger button's label, and what it says while it works. */
  confirm: string;
  busyLabel: string;
  disabled?: boolean;
  onConfirm: () => Promise<unknown>;
  /** Called once onConfirm succeeded; a promise it returns keeps the button busy. */
  onDone: () => unknown;
  onClose: () => void;
  children: ReactNode;
}) {
  const action = useAction();

  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-danger" onClick={() => action.run(onConfirm, onDone)} disabled={action.busy || disabled}>
            {action.busy ? busyLabel : confirm}
          </button>
        </>
      }
    >
      {children}
      {action.error && <Result tone="bad">{action.error}</Result>}
    </Modal>
  );
}

const menuItem = '[role="menuitem"]';

/**
 * A "⋯" button with a small menu of actions. Opening it focuses the first
 * item; the arrow keys, Home and End move between them; Escape or picking
 * one gives focus back to the button, Tab or a click elsewhere closes it.
 */
export function Menu({ label, items }: { label: string; items: { label: string; danger?: boolean; onSelect: () => void }[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    pop.current?.querySelector<HTMLElement>(menuItem)?.focus();

    const away = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      button.current?.focus();
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);

    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);

  const onKey = (e: ReactKeyboardEvent) => {
    if (e.key === "Tab") return setOpen(false);

    const all = [...(pop.current?.querySelectorAll<HTMLElement>(menuItem) ?? [])];
    const at = all.indexOf(document.activeElement as HTMLElement);
    const to = ({ ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: all.length - 1 } as Record<string, number>)[e.key];
    if (to === undefined || !all.length) return;

    e.preventDefault();
    all[(to + all.length) % all.length].focus();
  };

  return (
    <div className="menu" ref={ref}>
      <button type="button" className="icon-btn" ref={button} aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Icon name="more" />
      </button>
      {open && (
        <div className="menu-pop" role="menu" aria-label={label} ref={pop} onKeyDown={onKey}>
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              tabIndex={-1}
              className={it.danger ? "danger" : undefined}
              onClick={() => {
                setOpen(false);
                button.current?.focus();
                it.onSelect();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

let toastSeq = 0;
type ToastItem = { id: number; text: string; tone: "ok" | "bad" };
const listeners = new Set<(t: ToastItem) => void>();

export function toast(text: string, tone: "ok" | "bad" = "ok"): void {
  const t = { id: ++toastSeq, text, tone };
  listeners.forEach((l) => l(t));
}

export function Toasts() {
  const [items, setItems] = useState<ToastItem[]>([]);

  useEffect(() => {
    const on = (t: ToastItem) => {
      setItems((xs) => [...xs, t]);
      window.setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== t.id)), 4000);
    };
    listeners.add(on);

    return () => {
      listeners.delete(on);
    };
  }, []);

  return (
    <div className="toasts" aria-live="polite">
      {items.map((t) => (
        <div key={t.id} className={`toast toast-${t.tone}`}>
          <Icon name={t.tone === "ok" ? "check" : "alert"} />
          {t.text}
        </div>
      ))}
    </div>
  );
}
