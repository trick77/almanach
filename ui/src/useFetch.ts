import { useEffect, useState } from "react";
import { get } from "./api";

export interface Fetched<T> {
  data: T | null;
  failed: boolean;
}

/** useFetch loads one path once per path. Nothing here polls: the wiki
 *  changes once per merged pull request, and a reload is the refresh. The
 *  request is aborted when the path changes or the page unmounts, so a slow
 *  answer cannot land on a page that moved on; an answer for an earlier
 *  path is not shown as the answer to the current one. */
export function useFetch<T>(path: string): Fetched<T> {
  const [state, setState] = useState<Fetched<T> & { path: string | null }>({
    data: null,
    failed: false,
    path: null,
  });

  useEffect(() => {
    const controller = new AbortController();
    get<T>(path, controller.signal).then(
      (data) => setState({ data, failed: false, path }),
      (err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setState({ data: null, failed: true, path });
      },
    );
    return () => controller.abort();
  }, [path]);

  return state.path === path
    ? { data: state.data, failed: state.failed }
    : { data: null, failed: false };
}
