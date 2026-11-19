import { useCallback, useEffect, useRef, useState } from "react";
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
 *  that moved on. A failed refetch keeps the data and sets failed: the
 *  page says the data may be old. */
/** requestTimeout is how long one request may take before it is cut and
 *  counts as failed. */
export const requestTimeout = 60_000;

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
  // polled is set by the timer for the fetch it causes: that one is sent
  // as a poll, the first load and a reload are the reader's own.
  const polled = useRef(false);
  // pending is the request in flight, null once it settled. A poll while
  // one is pending is skipped, never sent: the new request's effect would
  // abort the old one, and an answer slower than the interval would then
  // never land.
  const pending = useRef<AbortController | null>(null);
  const poll = useCallback(() => {
    if (pending.current !== null || document.hidden) return;
    polled.current = true;
    setTick((n) => n + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    pending.current = controller;
    // A request that never settles (a stalled connection, a laptop that
    // slept) would hold every later poll: past requestTimeout it is cut and
    // counts as failed, and the next poll goes out.
    let timedOut = false;
    const timer = window.setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, requestTimeout);
    const settled = () => {
      window.clearTimeout(timer);
      if (pending.current === controller) pending.current = null;
    };
    const isPoll = polled.current;
    polled.current = false;
    get<T>(path, controller.signal, isPoll).then(
      (data) => {
        settled();
        setState({ data, failed: false, path });
      },
      () => {
        settled();
        // Aborted by the page moving on: nothing to say.
        if (controller.signal.aborted && !timedOut) return;
        setState((s) => ({
          data: s.path === path ? s.data : null,
          failed: true,
          path,
        }));
      },
    );
    return () => {
      settled();
      controller.abort();
    };
  }, [path, tick]);

  const shown = state.path === path ? state.data : null;
  const ms = typeof every === "function" ? every(shown) : every;
  // A hidden tab polls nothing; shown again, it reads once at once rather
  // than a whole interval later.
  useEffect(() => {
    if (ms <= 0) return;
    const id = window.setInterval(poll, ms);
    const visible = () => {
      if (!document.hidden) poll();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [ms, poll]);

  return state.path === path
    ? { data: state.data, failed: state.failed, reload }
    : { data: null, failed: false, reload };
}
