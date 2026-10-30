import { useCallback, useEffect, useState } from "react";
import { get } from "./api";

export interface Fetched<T> {
  data: T | null;
  failed: boolean;
  /** reload fetches the path again, keeping the data shown until the
   *  answer lands. */
  reload: () => void;
}

/** useFetch loads one path once per path, and again every `every`
 *  milliseconds when given (the Quellen pages, while a clone runs) or on
 *  reload; `every` may be a function of the data shown, so a page polls
 *  only while something runs. Zero polls nothing, which is what the wiki
 *  pages want: they change once per merged pull request. A refetch keeps
 *  the previous data on screen, so a table does not fall back to the
 *  skeleton every few seconds. The request is aborted when the path
 *  changes or the page unmounts, so a slow answer cannot land on a page
 *  that moved on. */
export function useFetch<T>(
  path: string,
  every: number | ((data: T | null) => number) = 0,
): Fetched<T> {
  const [state, setState] = useState<{
    data: T | null;
    failed: boolean;
    path: string | null;
  }>({ data: null, failed: false, path: null });
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((n) => n + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    get<T>(path, controller.signal).then(
      (data) => setState({ data, failed: false, path }),
      (err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setState((s) => ({
          data: s.path === path ? s.data : null,
          failed: true,
          path,
        }));
      },
    );
    return () => controller.abort();
  }, [path, tick]);

  const shown = state.path === path ? state.data : null;
  const ms = typeof every === "function" ? every(shown) : every;
  useEffect(() => {
    if (ms <= 0) return;
    const id = window.setInterval(reload, ms);
    return () => window.clearInterval(id);
  }, [ms, reload]);

  return state.path === path
    ? { data: state.data, failed: state.failed, reload }
    : { data: null, failed: false, reload };
}
