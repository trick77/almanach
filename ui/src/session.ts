import { useEffect, useState } from "react";
import type { Me } from "./api";
import { armSignIn, authMarkers, signedIn, signIn } from "./signin";
import { S } from "./strings";

/** The session gate. /api/me is the one request made
 *  before anything renders, so an expired cookie sends the browser to the
 *  provider instead of letting every page fail with its own 401.
 *
 *  "checking" is a real state: rendering first and redirecting afterwards
 *  flashes an empty wiki on every reload. "halted" keeps the automatic
 *  redirect from becoming a loop: a failed callback and a finished
 *  sign-out both land on a URL that says so, and the gate offers a button
 *  there instead of sending the browser back to a provider that returns
 *  it to the same place. A check that could not be made halts too, with a
 *  button that tries again. */
export type Session =
  | { state: "checking" }
  | { state: "in"; me: Me }
  | { state: "out" }
  | { state: "halted"; message: string; action: HaltAction };

/** HaltAction is the halted page's button: signIn and again both go to the
 *  login ("Anmelden", "Erneut versuchen"), retry reloads the page. */
export type HaltAction = "signIn" | "again" | "retry";

/** haltReason is why the gate must not redirect on its own, asked only in
 *  oidc mode without a session:
 *  - after a failed callback the provider still has a live session, answers
 *    the next login without a prompt, and the callback fails the same way;
 *  - after sign-out the provider's session is untouched and hands out a new
 *    token unseen: the button would appear to do nothing;
 *  - while the provider has not answered discovery, the login would only
 *    come back here. */
export function haltReason(
  search: string,
): { message: string; action: HaltAction } | null {
  const params = new URLSearchParams(search);
  if (params.has("signed_out"))
    return { message: S.auth.signedOut, action: "signIn" };
  if (params.get("auth_error") === "provider_unavailable")
    return { message: S.auth.unavailable, action: "again" };
  if (params.get("auth_error") === "provider_misconfigured")
    return { message: S.auth.misconfigured, action: "again" };
  if (params.has("auth_error"))
    return { message: S.auth.failed, action: "signIn" };
  return null;
}

/** stripMarkers drops the markers from the address once they mean
 *  nothing: with a session, or in none mode. */
function stripMarkers() {
  const params = new URLSearchParams(window.location.search);
  if (!authMarkers.some((m) => params.has(m))) return;
  for (const m of authMarkers) params.delete(m);
  const q = params.toString();
  window.history.replaceState(
    window.history.state,
    "",
    window.location.pathname + (q ? `?${q}` : ""),
  );
}

export function useSession(): Session {
  const [session, setSession] = useState<Session>({ state: "checking" });

  useEffect(() => {
    let cancelled = false;
    const settle = (s: Session) => {
      if (cancelled) return;
      armSignIn(s.state === "in" && s.me.mode === "oidc");
      setSession(s);
    };
    void (async () => {
      let res: Response;
      try {
        res = await fetch("/api/me");
      } catch {
        // A network error says nothing about the session or the mode:
        // redirecting would bounce the reader to the provider whenever the
        // connection drops, and rendering would assume none mode. Halt
        // with a button that tries again.
        settle({ state: "halted", message: S.auth.offline, action: "retry" });
        return;
      }
      if (cancelled) return;
      if (res.status === 401) {
        const halt = haltReason(window.location.search);
        if (halt !== null) {
          settle({ state: "halted", ...halt });
          return;
        }
        // A full navigation, not a fetch: the provider answers with a login
        // page and a redirect chain a fetch cannot follow.
        if (signIn()) settle({ state: "out" });
        else
          settle({ state: "halted", message: S.auth.loop, action: "signIn" });
        return;
      }
      if (!res.ok) {
        settle({
          state: "halted",
          message: S.auth.unchecked(res.status),
          action: "retry",
        });
        return;
      }
      let me: Me;
      try {
        me = (await res.json()) as Me;
      } catch {
        // A 200 that is no JSON: a proxy's page where the answer should be.
        settle({
          state: "halted",
          message: S.auth.unreadable,
          action: "retry",
        });
        return;
      }
      signedIn();
      stripMarkers();
      settle({ state: "in", me });
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return session;
}

/** logout clears the cookie and goes where the server says: a page that
 *  halts, since the provider's session lives on. false when the server
 *  did not confirm it: the reader stays on the page, still signed in, and
 *  the shell says the sign-out failed. */
export async function logout(): Promise<boolean> {
  try {
    const res = await fetch("/api/auth/logout", { method: "POST" });
    if (!res.ok) return false;
    const body = (await res.json()) as { redirect_url?: string };
    window.location.href = body.redirect_url ?? "/?signed_out=1";
    return true;
  } catch {
    return false;
  }
}
