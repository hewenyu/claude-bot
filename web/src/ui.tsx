import { useEffect, useRef, useState, type ReactNode } from "react";
import { channel } from "./format";
import { ChannelLogo, hasLogo } from "./logos";

/** The channel's official logo; a lettered tile for a channel without one. */
export function ChannelBadge({ name, size = "md" }: { name: string; size?: "sm" | "md" }) {
  const c = channel(name);

  if (hasLogo(name)) {
    return (
      <span className={`badge badge-${size} badge-logo`} title={c.label} aria-hidden>
        <ChannelLogo name={name} />
      </span>
    );
  }

  return (
    <span className={`badge badge-${size}`} title={c.label} aria-hidden>
      {name.slice(0, 2).toUpperCase()}
    </span>
  );
}

export function Dot({ tone, pulse }: { tone: "ok" | "warn" | "bad" | "off" | "info"; pulse?: boolean }) {
  return <span className={`dot dot-${tone}${pulse ? " dot-pulse" : ""}`} aria-hidden />;
}

/** Copies text; says so for a moment. */
export function CopyButton({ text, label = "复制" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);

  useEffect(() => {
    if (!done) return;
    const t = window.setTimeout(() => setDone(false), 1500);

    return () => window.clearTimeout(t);
  }, [done]);

  return (
    <button
      type="button"
      className="btn btn-ghost btn-xs"
      onClick={() => navigator.clipboard.writeText(text).then(() => setDone(true))}
      aria-label={`${label}：${text}`}
    >
      {done ? "已复制" : label}
    </button>
  );
}

/** A command to run in a terminal, with a copy button. */
export function Command({ children }: { children: string }) {
  return (
    <div className="command">
      <code>{children}</code>
      <CopyButton text={children} />
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
      {children}
    </div>
  );
}

/** A dialog; Escape or the backdrop closes it. */
export function Modal({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, select, button")?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);

    return () => {
      document.removeEventListener("keydown", onKey);
      prev?.focus();
    };
  }, [onClose]);

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} ref={ref}>
        <div className="modal-head">
          <h2>{title}</h2>
          <button type="button" className="btn btn-ghost btn-icon" onClick={onClose} aria-label="关闭">
            ×
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
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
          {t.text}
        </div>
      ))}
    </div>
  );
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
