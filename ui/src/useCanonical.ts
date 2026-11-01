import { useEffect } from "react";
import { replaceTo } from "./routing";

/** useCanonical puts a page's path under the current names in the address
 *  bar once the page has loaded: the API finds a project or repository by
 *  any name it ever had and answers with the current one, so a bookmark or
 *  a PR chip under an old name lands on the new. null: not loaded yet. */
export function useCanonical(path: string | null) {
  useEffect(() => {
    if (path !== null) replaceTo(path);
  }, [path]);
}
