import { useCallback, useEffect, useRef, useState } from "react";
import { errorText } from "./format";

/** How often the page polls: the overview drives the orb and the badges, task lists can lag a little. */
export const POLL_FAST = 3000;
export const POLL_SLOW = 5000;

export interface Loaded<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  /** Loads again now; resolves once the new data (or error) is in. */
  reload: () => Promise<void>;
}

/**
 * Loads with fetcher and, if every is set, again every so often while the
 * page is visible. A failed refresh keeps the last data and reports the
 * error. Changing key starts over.
 */
export function usePoll<T>(fetcher: () => Promise<T>, every: number | null, key: string): Loaded<T> {
  const [state, setState] = useState<{ key: string; data?: T; error?: unknown; loading: boolean }>({
    key,
    loading: true,
  });
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;
  const seq = useRef(0);

  const load = useCallback(() => {
    const n = ++seq.current;

    return fetcherRef.current().then(
      (data) => {
        if (n === seq.current) setState({ key, data, loading: false });
      },
      (error) => {
        if (n === seq.current) setState((s) => ({ ...s, key, error, loading: false }));
      },
    );
  }, [key]);

  useEffect(() => {
    setState((s) => (s.key === key ? s : { key, loading: true }));
    load();

    if (every === null) return;

    const tick = () => document.visibilityState === "visible" && load();
    const timer = window.setInterval(tick, every);
    document.addEventListener("visibilitychange", tick);

    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [load, every, key]);

  const current = state.key === key ? state : { loading: true, data: undefined, error: undefined };

  return { data: current.data, error: current.error, loading: current.loading, reload: load };
}

/** Re-renders every so often, for relative times. */
export function useNow(every = 30_000): number {
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), every);

    return () => window.clearInterval(t);
  }, [every]);

  return now;
}

export interface Action {
  /** A run is under way. */
  busy: boolean;
  /** Why the last run failed; cleared when the next one starts. */
  error: string | null;
  /**
   * Runs fn; once it succeeds, onOk with what it returned. Busy lasts until
   * a promise onOk returns settles, so a dialog that leaves the page when
   * done cannot be submitted twice meanwhile.
   */
  run: <T>(fn: () => Promise<T>, onOk?: (v: T) => unknown) => void;
}

/**
 * A button's request: busy while it runs, and why it failed. With onError,
 * failures go there instead of error.
 */
export function useAction(onError?: (message: string) => void): Action {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;

  const run = useCallback(<T>(fn: () => Promise<T>, onOk?: (v: T) => unknown) => {
    setBusy(true);
    setError(null);
    fn().then(
      (v) => Promise.resolve(onOk?.(v)).finally(() => setBusy(false)),
      (e) => {
        setBusy(false);
        if (onErrorRef.current) onErrorRef.current(errorText(e));
        else setError(errorText(e));
      },
    );
  }, []);

  return { busy, error, run };
}
