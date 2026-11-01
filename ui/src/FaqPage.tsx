import { useEffect } from "react";
import { S } from "./strings";
import { column, eyebrow, h2, page, Prose } from "./ui";

export function FaqPage() {
  useEffect(() => {
    document.title = `${S.faq.title} ${S.common.sep} ${S.app}`;
  }, []);
  const qa = [
    [S.faq.what, S.faq.whatText],
    [S.faq.who, S.faq.whoText],
    [S.faq.sources, S.faq.sourcesText],
    [S.faq.clones, S.faq.clonesText],
  ];
  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.faq.title}</h2>
        {qa.map(([q, a]) => (
          <section key={q}>
            <div className={eyebrow}>{q}</div>
            <Prose
              text={a}
              className="font-serif text-[16px] leading-[1.6] text-ink-prose"
            />
          </section>
        ))}
      </div>
    </div>
  );
}
