import type { ReactNode } from 'react';
import { ArrowUpRight } from 'lucide-react';
import { useSlots } from '../context';
import { defaultExamples, DOCS } from '../lib/examples';
import type { Examples } from '../context';
import { CodeTabs, type CodeTab } from './CodeTabs';

type Props = {
  /** "No queues yet". */
  title: string;
  /** What it is and how it comes about, in a sentence or two. */
  intro: ReactNode;
  /** The same first step from Python, Node and curl. */
  code: (e: Examples) => CodeTab[];
  /** The same as configuration, deployed with `workly deploy`. */
  yaml: (e: Examples) => string;
  yamlIntro: ReactNode;
  /** Docs pages, opened in a new tab: [label, path under /docs]. */
  docs: [string, string][];
};

/**
 * A page's empty state: how to make its first one, from code first, then as
 * configuration in workly.yaml, and where to read more. The examples point
 * at this server, or where a wrapping app says (Examples).
 */
export function StartHere({ title, intro, code, yaml, yamlIntro, docs }: Props) {
  const e = useSlots().examples ?? defaultExamples();
  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h3 className="font-medium">{title}</h3>
        <p className="text-sm text-muted-foreground">{intro}</p>
      </div>
      {e.setup && <div className="text-sm text-muted-foreground">{e.setup}</div>}
      <section className="flex flex-col gap-2">
        <h4 className="text-sm font-medium">From your code</h4>
        <CodeTabs tabs={code(e)} />
        {!e.apiKey && (
          <p className="text-xs text-faint">
            The echo endpoint answers any request; it's there with <span className="font-mono">workly dev</span>.
          </p>
        )}
      </section>
      <section className="flex flex-col gap-2">
        <h4 className="text-sm font-medium">Or as configuration</h4>
        <p className="text-sm text-muted-foreground">{yamlIntro}</p>
        <CodeTabs tabs={[{ label: 'workly.yaml', lang: 'yaml', code: yaml(e) }]} />
      </section>
      <div className="flex flex-wrap gap-x-5 gap-y-1 text-sm">
        {docs.map(([label, path]) => (
          <a
            key={path}
            href={`${DOCS}/${path}/`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground hover:decoration-foreground"
          >
            {label}
            <ArrowUpRight className="size-3.5" />
          </a>
        ))}
      </div>
    </div>
  );
}
