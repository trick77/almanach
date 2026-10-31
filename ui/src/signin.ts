// The way back to the provider, shared by the session gate and every API
// call: in oidc mode a 401 means the session ran out, and the answer is a
// full navigation to the login, never a page that says "sign-in required".

/** The URL markers of a failed callback and a finished sign-out: on them
 *  the gate halts with a button instead of redirecting. */
export const authMarkers = ["signed_out", "auth_error"] as const;

const stampKey = "almanach.signin";

/** loopWindow is how soon after an automatic sign-in another one counts as
 *  a loop: a session that is gone again right after the callback (a cookie
 *  the browser drops) would otherwise bounce the reader to the provider
 *  forever. */
const loopWindow = 30_000;

let armed = false;

/** armSignIn makes a 401 from any API call sign in again: on in oidc mode
 *  once the gate let the reader in, off everywhere else (none mode, a
 *  halted page). */
export function armSignIn(on: boolean): void {
  armed = on;
}

/** onUnauthorized is what an API call does with its status. */
export function onUnauthorized(status: number): void {
  if (status === 401 && armed) signIn();
}

/** signIn navigates to the login, unless the page carries a marker or the
 *  last automatic sign-in was a moment ago; false when it did not go. The
 *  page it leaves goes along as ?next=, so the reader lands back on it. */
export function signIn(): boolean {
  const params = new URLSearchParams(window.location.search);
  if (authMarkers.some((m) => params.has(m))) return false;
  const now = Date.now();
  const last = Number(window.sessionStorage.getItem(stampKey) ?? 0);
  if (now - last < loopWindow) return false;
  window.sessionStorage.setItem(stampKey, String(now));
  armed = false;
  const q = params.toString();
  const next = window.location.pathname + (q ? `?${q}` : "");
  window.location.href =
    next === "/"
      ? "/api/auth/login"
      : `/api/auth/login?next=${encodeURIComponent(next)}`;
  return true;
}

/** signedIn forgets the last automatic sign-in: it worked. */
export function signedIn(): void {
  window.sessionStorage.removeItem(stampKey);
}
