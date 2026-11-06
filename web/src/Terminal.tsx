// An agent's terminal, as the gateway owns it: what it printed so far, then
// live; keys go to the agent as if typed in its own terminal.

import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef } from "react";
import { openTerminal } from "./api";

// How the gateway closes the stream (cmd/claude-bot/gateway_web_runner.go).
const CLOSE_RESYNC = 4000; // fell behind: replay from the start
const CLOSE_EXITED = "exited"; // the program ended

const RETRY_MS = 2000;
const RESIZE_DEBOUNCE_MS = 120;
const SCROLLBACK = 5000;

// The terminal is always dark, in both themes: Claude Code draws for one.
const theme = {
  background: "#111113",
  foreground: "#e6e6ea",
  cursor: "#8484f0",
  selectionBackground: "#34345e",
  black: "#1a1a1d",
  brightBlack: "#6b6b76",
};

/**
 * Shows an agent's terminal and types into it. An agent that ended and is
 * resumed keeps its id: when live turns true again, it reattaches.
 */
export function Terminal({ agent, live }: { agent: string; live: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const reattach = useRef<(() => void) | null>(null);

  useEffect(() => {
    const el = host.current;
    if (!el) return;

    const term = new XTerm({
      fontFamily: '"SF Mono", "JetBrains Mono", Menlo, Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.15,
      cursorBlink: true,
      scrollback: SCROLLBACK,
      theme,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);

    const enc = new TextEncoder();
    let ws: WebSocket | null = null;
    let closed = false;
    let timer: number | undefined;

    const sendSize = () => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };

    const open = () => {
      if (closed) return;
      window.clearTimeout(timer);

      const sock = openTerminal(agent);
      term.reset();
      ws = sock;
      sock.onopen = () => {
        fit.fit();
        sendSize();
      };
      sock.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      };
      sock.onclose = (e) => {
        if (ws === sock) ws = null;
        if (closed) return;
        if (e.code === CLOSE_RESYNC) open();
        else if (e.reason !== CLOSE_EXITED) timer = window.setTimeout(open, RETRY_MS);
      };
    };

    reattach.current = () => {
      if (!ws) open();
    };

    const input = term.onData((d) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    const binary = term.onBinary((d) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff));
    });

    let resizeTimer: number | undefined;
    const observer = new ResizeObserver(() => {
      window.clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(() => {
        if (el.clientWidth === 0) return;
        fit.fit();
        sendSize();
      }, RESIZE_DEBOUNCE_MS);
    });
    observer.observe(el);

    fit.fit();
    open();

    return () => {
      closed = true;
      reattach.current = null;
      window.clearTimeout(timer);
      window.clearTimeout(resizeTimer);
      observer.disconnect();
      input.dispose();
      binary.dispose();
      ws?.close();
      term.dispose();
    };
  }, [agent]);

  useEffect(() => {
    if (live) reattach.current?.();
  }, [live]);

  return <div className="term" ref={host} />;
}
