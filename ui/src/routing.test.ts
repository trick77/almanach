import { describe, expect, it } from "vitest";
import { factIds, factPath } from "./repoFacts";
import {
  notePath,
  notePathFor,
  projectPath,
  replaceTo,
  repoPath,
  revisionPath,
  routeFromPath,
  searchFor,
  sourcePath,
} from "./routing";

describe("routeFromPath", () => {
  it("reads every page and falls back to the projects list", () => {
    expect(routeFromPath("/")).toEqual({ page: "projects" });
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
    expect(routeFromPath("/projects/PLAT/revisions/3")).toEqual({
      page: "revision",
      key: "PLAT",
      slug: "",
      n: 3,
    });
    expect(routeFromPath("/projects/PLAT/repos/files/revisions/12")).toEqual({
      page: "revision",
      key: "PLAT",
      slug: "files",
      n: 12,
    });
    expect(routeFromPath("/projects/PLAT/revisions/0")).toEqual({
      page: "projects",
    });
    expect(revisionPath("PLAT", "", 3)).toBe("/projects/PLAT/revisions/3");
    expect(revisionPath("PLAT", "files", 12)).toBe(
      "/projects/PLAT/repos/files/revisions/12",
    );
    expect(routeFromPath("/sources")).toEqual({ page: "sources" });
    expect(routeFromPath("/queue")).toEqual({ page: "queue" });
    expect(routeFromPath("/queue/x")).toEqual({ page: "projects" });
    expect(routeFromPath("/dependencies")).toEqual({ page: "deps" });
    expect(routeFromPath("/dependencies/x")).toEqual({ page: "projects" });
    expect(routeFromPath("/costs")).toEqual({ page: "costs" });
    expect(routeFromPath("/costs/x")).toEqual({ page: "projects" });
    // The search page carries its phrase in the query string, decoded;
    // none is an empty phrase.
    expect(routeFromPath("/search", "?q=Geb%C3%BChren%20limit")).toEqual({
      page: "search",
      q: "Gebühren limit",
    });
    expect(routeFromPath("/search")).toEqual({ page: "search", q: "" });
    expect(routeFromPath("/search/x")).toEqual({ page: "projects" });
    expect(searchFor("Gebühren limit")).toBe("/search?q=Geb%C3%BChren%20limit");
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
      // The FAQ page: removed, the server answers 404.
      "/faq",
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

  it("builds a fact table's link: the reference tab at the section", () => {
    expect(factPath("PAY", "pay-api", "messages")).toBe(
      "/projects/PAY/repos/pay-api#description/messages",
    );
    expect(factPath("PAY", "pay-api", "interfaces")).toBe(
      "/projects/PAY/repos/pay-api#description/interfaces",
    );
    expect(factPath("a b", "pay-api", "deps")).toBe(
      "/projects/a%20b/repos/pay-api#description/dependencies",
    );
    // The section ids the repository page gives its sections.
    expect(factIds).toEqual({
      deps: "dependencies",
      parts: "parts",
      interfaces: "interfaces",
      messages: "messages",
    });
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
