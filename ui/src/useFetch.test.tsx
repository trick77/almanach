import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { pollHeader } from "./api";
import { useFetch } from "./useFetch";

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
});
