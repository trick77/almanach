import { describe, expect, it } from "vitest";
import {
  notePath,
  notePathFor,
  projectPath,
  replaceTo,
  repoPath,
  routeFromPath,
  sourcePath,
} from "./routing";

describe("routeFromPath", () => {
  it("reads every page and falls back to the projects list", () => {
    expect(routeFromPath("/")).toEqual({ page: "projects" });
    expect(routeFromPath("/faq")).toEqual({ page: "faq" });
    expect(routeFromPath("/projects/PLAT")).toEqual({
      page: "project",
      key: "PLAT",
    });
    expect(routeFromPath("/projects/pay.v2/repos/pay-api")).toEqual({
      page: "repo",
      key: "pay.v2",
      slug: "pay-api",
    });
    expect(routeFromPath("/projects/PLAT/repos/files/notes/42")).toEqual({
      page: "note",
      key: "PLAT",
      slug: "files",
      pr: 42,
    });
    expect(routeFromPath("/sources")).toEqual({ page: "sources" });
    expect(routeFromPath("/queue")).toEqual({ page: "queue" });
    expect(routeFromPath("/queue/x")).toEqual({ page: "projects" });
    expect(routeFromPath("/dependencies")).toEqual({ page: "deps" });
    expect(routeFromPath("/dependencies/x")).toEqual({ page: "projects" });
    expect(routeFromPath("/sources/~jan")).toEqual({
      page: "source",
      key: "~jan",
    });
    expect(routeFromPath("/sources/a b")).toEqual({ page: "projects" });
    expect(sourcePath("a b")).toBe("/sources/a%20b");
    for (const p of [
      "/nope",
      "/projects",
      "/projects/PLAT/repos",
      "/projects/PLAT/repos/files/notes/0",
      "/projects/PLAT/repos/files/notes/x",
    ]) {
      expect(routeFromPath(p)).toEqual({ page: "projects" });
    }
  });

  it("builds the paths it reads", () => {
    expect(projectPath("PLAT")).toBe("/projects/PLAT");
    expect(repoPath("PLAT", "files")).toBe("/projects/PLAT/repos/files");
    expect(notePath("PLAT", "files", 42)).toBe(
      "/projects/PLAT/repos/files/notes/42",
    );
    expect(notePathFor("PLAT/files#42")).toBe(
      "/projects/PLAT/repos/files/notes/42",
    );
    expect(notePathFor("garbage")).toBeNull();
    expect(notePathFor("PLAT/files#0")).toBeNull();
    // Exactly the server's allowlist: a key the shell would 404 on is no link.
    expect(notePathFor("~jan/scratch#3")).toBe(
      "/projects/~jan/repos/scratch/notes/3",
    );
    expect(notePathFor("a b/files#3")).toBeNull();
    expect(notePathFor("PLAT/~x#3")).toBeNull();
    // Builders encode, so a bad key is a visible 404 rather than a silent miss.
    expect(repoPath("a b", "files")).toBe("/projects/a%20b/repos/files");
  });
});

describe("replaceTo", () => {
  it("swaps the path for the current name and keeps the tab's hash", () => {
    window.history.replaceState({}, "", "/projects/OLD#description");
    replaceTo("/projects/NEW");
    expect(window.location.pathname).toBe("/projects/NEW");
    expect(window.location.hash).toBe("#description");
    window.history.replaceState({}, "", "/");
  });
});
