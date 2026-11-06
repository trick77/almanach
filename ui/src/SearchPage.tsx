import { useEffect } from "react";
import type { SearchHit, SearchPage as Hit, SearchResult } from "./api";
import { kindLabel, statementId, summaryId } from "./Description";
import { refName } from "./format";
import { projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Empty,
  Failed,
  Link,
  Marked,
  Skeleton,
  column,
  eyebrow,
  h2,
  lede,
  mono,
  page,
} from "./ui";

/** The shortest phrase the server searches; a shorter one is said so
 *  here, without a request. */
const minChars = 2;

/** SearchPage is the hits of one phrase, grouped by page in the wiki's
 *  order: the page's name heads its hits, the summary's first, each a
 *  link to the statement on its page (its deep link) with the text
 *  around the hit, the phrase marked. The phrase lives in the address
 *  (?q=): a search is a page to share and to come back to. */
export function SearchPage({ q }: { q: string }) {
  // Its spaces collapsed, as the server matches it: the marks find what
  // it found.
  const phrase = q.trim().split(/\s+/).join(" ");
  useEffect(() => {
    document.title = `${S.search.title} ${S.common.sep} ${S.app}`;
  }, []);
  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>
          {S.search.title}
          {phrase !== "" && (
            <span className="ml-3 font-sans text-[17px] font-normal text-muted">
              {S.search.quoted(phrase)}
            </span>
          )}
        </h2>
        {[...phrase].length < minChars ? (
          <p className={lede}>{S.search.tooShort}</p>
        ) : (
          <Results phrase={phrase} />
        )}
      </div>
    </div>
  );
}

/** Results asks the server for the phrase, one request per phrase. */
function Results({ phrase }: { phrase: string }) {
  const { data, failed } = useFetch<SearchResult>(
    `search?q=${encodeURIComponent(phrase)}`,
  );
  if (failed) return <Failed message={S.search.failed} />;
  if (data === null) return <Skeleton />;
  if (data.pages.length === 0) return <Empty>{S.search.none(phrase)}</Empty>;
  const hits = data.pages.reduce(
    (n, p) => n + p.statements.length + (p.summary !== undefined ? 1 : 0),
    0,
  );
  return (
    <>
      <p className={lede}>{S.search.hits(hits, data.pages.length)}</p>
      {data.pages.map((p) => (
        <PageHits key={pageKey(p)} p={p} query={phrase} />
      ))}
      {data.more > 0 && (
        <p className="mt-6 text-[13.5px] text-muted">
          {S.search.more(data.more)}
        </p>
      )}
    </>
  );
}

const pageKey = (p: Hit) => p.scope + "|" + p.project_key + "|" + p.repo_slug;

/** anchor is a hit's deep link on its page: the description tab, then
 *  the element to scroll to (useHashTab's "tab/id"). */
const anchor = (id: string) => `#description/${id}`;

/** pagePath is where a hit's page is: a project of one's repository page
 *  at the project's path. */
function pagePath(p: Hit): string {
  return p.scope === "project" || p.single
    ? projectPath(p.project_key)
    : repoPath(p.project_key, p.repo_slug ?? "");
}

/** pageName names a hit's page: a project's by its key, a repository's as
 *  the pages name one (refName); a project of one by its key alone. */
function pageName(p: Hit): string {
  if (p.scope === "project" || p.single) return S.search.project(p.project_key);
  return refName(p.project_key, p.repo_slug ?? "");
}

/** PageHits is one page's hits under its name. */
function PageHits({ p, query }: { p: Hit; query: string }) {
  const at = pagePath(p);
  return (
    <section>
      <div className={eyebrow}>
        <Link to={at} className="text-ink-dim hover:text-ink">
          {pageName(p)}
        </Link>
      </div>
      <ol className="m-0 list-none p-0">
        {p.summary !== undefined && (
          <Row
            to={at + anchor(summaryId)}
            label=""
            title={S.rail.summary}
            snippet={p.summary}
            query={query}
          />
        )}
        {p.statements.map((s: SearchHit) => (
          <Row
            key={s.id}
            to={at + anchor(statementId(s.id))}
            label={kindLabel(s.kind, p.scope === "project")}
            title={s.name}
            area={s.area}
            snippet={s.snippet}
            file={s.file}
            query={query}
          />
        ))}
      </ol>
    </section>
  );
}

/** Row is one hit: its kind, its name as the link, the text around the
 *  hit under it, the phrase marked throughout. */
function Row({
  to,
  label,
  title,
  area,
  snippet,
  file,
  query,
}: {
  to: string;
  label: string;
  title: string;
  area?: string;
  snippet: string;
  /** The file that matched, shown where the snippet would be. */
  file?: string;
  query: string;
}) {
  return (
    <li className="border-b border-border-soft py-2.5 text-[13.5px]">
      <div className="flex flex-wrap items-baseline gap-x-2">
        {label !== "" && (
          <span className="text-[11px] font-medium tracking-[.1em] text-faint uppercase">
            {label}
          </span>
        )}
        <Link to={to} className="font-semibold text-ink hover:underline">
          <Marked text={title} query={query} />
        </Link>
        {area !== undefined && area !== "" && (
          <span className="text-muted">
            <Marked text={area} query={query} />
          </span>
        )}
      </div>
      {snippet !== "" && (
        <p className="mt-0.5 max-w-[75ch] font-serif text-[15px] leading-[1.5] text-ink-dim">
          <Marked text={snippet} query={query} />
        </p>
      )}
      {file !== undefined && (
        <p className={"mt-0.5 text-muted " + mono}>
          <Marked text={file} query={query} />
        </p>
      )}
    </li>
  );
}
