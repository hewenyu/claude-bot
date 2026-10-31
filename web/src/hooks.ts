import { useCallback, useEffect, useRef, useState } from "react";

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
