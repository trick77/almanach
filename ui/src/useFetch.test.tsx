import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { pollHeader } from "./api";
import { requestTimeout, useFetch } from "./useFetch";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function polls(calls: unknown[][]): boolean[] {
  return calls.map((c) => {
    const init = c[1] as RequestInit | undefined;
    const headers = (init?.headers ?? {}) as Record<string, string>;
    return headers[pollHeader] === "1";
  });
}

describe("useFetch", () => {
  // The first load and a reload are the reader's; only a timer's refetch
  // is sent as a poll, which the server logs at Debug.
  it("marks only the timer's refetches as polls", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ n: 1 }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useFetch<{ n: number }>("queue", 1000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await act(async () => {
      result.current.reload();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetchMock.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
      "/api/queue",
      "/api/queue",
      "/api/queue",
    ]);
    expect(polls(fetchMock.mock.calls as unknown[][])).toEqual([
      false,
      true,
      false,
    ]);
  });

  // An answer slower than the interval: the timer skips while it is out,
  // so it lands, instead of each tick aborting the one before.
  it("lets a slow answer land", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn(
      (_url: string, init?: RequestInit) =>
        new Promise((resolve, reject) => {
          const t = setTimeout(
            () =>
              resolve({ ok: true, status: 200, json: async () => ({ n: 1 }) }),
            2500,
          );
          init?.signal?.addEventListener("abort", () => {
            clearTimeout(t);
            reject(new DOMException("aborted", "AbortError"));
          });
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useFetch<{ n: number }>("queue", 1000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2600);
    });
    expect(result.current.data).toEqual({ n: 1 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  // A request that never settles is cut at requestTimeout, counts as
  // failed, and polling goes on.
  it("cuts a request that never settles and polls again", async () => {
    vi.useFakeTimers();
    let calls = 0;
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      calls++;
      if (calls === 1)
        return new Promise((_, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          ),
        );
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => ({ n: 2 }),
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    // An interval past the cut: the failure is seen before the next poll.
    const every = requestTimeout * 2;
    const { result } = renderHook(() =>
      useFetch<{ n: number }>("queue", every),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(requestTimeout);
    });
    expect(result.current.failed).toBe(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(every - requestTimeout);
    });
    expect(result.current.data).toEqual({ n: 2 });
    expect(result.current.failed).toBe(false);
  });

  // A hidden tab polls nothing; shown again, it reads at once.
  it("polls nothing while the tab is hidden", async () => {
    vi.useFakeTimers();
    let hidden = false;
    vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ n: 1 }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    renderHook(() => useFetch<{ n: number }>("queue", 1000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    hidden = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    hidden = false;
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
