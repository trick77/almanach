import { describe, expect, it } from "vitest";
import {
  notePath,
  notePathFor,
  projectPath,
  repoPath,
  routeFromPath,
} from "./routing";

describe("routeFromPath", () => {
  it("reads every page and falls back to the projects list", () => {
    expect(routeFromPath("/")).toEqual({ page: "projects" });
    expect(routeFromPath("/faq")).toEqual({ page: "faq" });
    expect(routeFromPath("/projekte/PLAT")).toEqual({
      page: "project",
      key: "PLAT",
    });
    expect(routeFromPath("/projekte/pay.v2/repos/pay-api")).toEqual({
      page: "repo",
      key: "pay.v2",
      slug: "pay-api",
    });
    expect(routeFromPath("/projekte/PLAT/repos/files/notizen/42")).toEqual({
      page: "note",
      key: "PLAT",
      slug: "files",
      pr: 42,
    });
    for (const p of [
      "/nope",
      "/projekte",
      "/projekte/PLAT/repos",
      "/projekte/PLAT/repos/files/notizen/0",
      "/projekte/PLAT/repos/files/notizen/x",
    ]) {
      expect(routeFromPath(p)).toEqual({ page: "projects" });
    }
  });

  it("builds the paths it reads", () => {
    expect(projectPath("PLAT")).toBe("/projekte/PLAT");
    expect(repoPath("PLAT", "files")).toBe("/projekte/PLAT/repos/files");
    expect(notePath("PLAT", "files", 42)).toBe(
      "/projekte/PLAT/repos/files/notizen/42",
    );
    expect(notePathFor("PLAT/files#42")).toBe(
      "/projekte/PLAT/repos/files/notizen/42",
    );
    expect(notePathFor("garbage")).toBeNull();
    expect(notePathFor("PLAT/files#0")).toBeNull();
  });
});
