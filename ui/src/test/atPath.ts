import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

/** atPath starts each test of the suite (or describe) it is called in at
 *  path, a page's canonical one, and resets the address after.
 *
 *  A page rendered at "/" moves the address to its canonical path in a
 *  passive effect with a popstate, which under load lands at any point of
 *  a test: after it read the path, before a tab wrote its hash, over a step
 *  to another page. At its canonical path the replace never runs. The page
 *  is unmounted before the reset, so a replace still pending carries no
 *  path into the next test. */
export function atPath(path: string): void {
  beforeEach(() => window.history.replaceState({}, "", path));
  afterEach(() => {
    cleanup();
    window.history.replaceState({}, "", "/");
  });
}
