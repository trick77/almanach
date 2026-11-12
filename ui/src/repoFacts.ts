// The repository page's facts from the code, on its reference tab: their
// sections' ids, the page's anchors and a fact table's links to them. Not in
// RepoPage, which imports the tables that link here.
import { DESCRIPTION } from "./Description";
import { anchorHash } from "./Rail";
import { repoPath } from "./routing";

export const factIds = {
  deps: "dependencies",
  parts: "parts",
  interfaces: "interfaces",
  messages: "messages",
} as const;

export type Fact = keyof typeof factIds;

/** factPath is a repository's page on its reference tab at one kind of its
 *  facts: where a fact table's or drawing's repository link lands. A page
 *  without the section opens on the tab alone. */
export function factPath(key: string, slug: string, fact: Fact): string {
  return repoPath(key, slug) + anchorHash(DESCRIPTION, factIds[fact]);
}
