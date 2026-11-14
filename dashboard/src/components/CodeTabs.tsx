import { useEffect, useState } from 'react';
import { cn } from 'cn';
import type { CodeLang } from '../lib/highlight';
import { Tabs, TabsList, TabsTab } from '../primitives/tabs';
import { CopyButton } from './CopyButton';

export type CodeTab = { label: string; lang: CodeLang; code: string };

// One choice for every panel: someone reading Python wants Python next time too.
const KEY = 'workly.codeTab';

function remembered(tabs: CodeTab[]): string {
  try {
    const label = localStorage.getItem(KEY);
    if (label && tabs.some((t) => t.label === label)) return label;
  } catch {
    // Storage can be off; the first tab will do.
  }
  return tabs[0].label;
}

/**
 * The same code in a few languages: a code panel with a tab per language in
 * its title bar, highlighted, with a copy button for the tab shown.
 */
export function CodeTabs({ tabs, className }: { tabs: CodeTab[]; className?: string }) {
  const [active, setActive] = useState(() => remembered(tabs));
  const tab = tabs.find((t) => t.label === active) ?? tabs[0];
  const choose = (label: string) => {
    setActive(label);
    try {
      localStorage.setItem(KEY, label);
    } catch {
      // Only a convenience.
    }
  };

  return (
    <div className={cn('overflow-hidden rounded-lg border border-border bg-card', className)}>
      <div className="flex items-center justify-between gap-2 border-b border-border pr-1.5 pl-3">
        <Tabs value={tab.label} onValueChange={(v) => choose(String(v))}>
          <TabsList className="gap-3 shadow-none">
            {tabs.map((t) => (
              <TabsTab key={t.label} value={t.label} className="h-8 text-xs">
                {t.label}
              </TabsTab>
            ))}
          </TabsList>
        </Tabs>
        <CopyButton text={tab.code} title={`Copy ${tab.label}`} />
      </div>
      <Highlighted code={tab.code} lang={tab.lang} />
    </div>
  );
}

const PRE = 'max-h-96 overflow-auto p-3 font-mono text-xs leading-relaxed';

/** Plain text at once, coloured as soon as the highlighter has loaded. */
export function Highlighted({ code, lang }: { code: string; lang: CodeLang }) {
  const [html, setHtml] = useState<{ code: string; lang: CodeLang; html: string }>();
  useEffect(() => {
    let live = true;
    import('../lib/highlight')
      .then(({ highlight }) => highlight(code, lang))
      .then((h) => live && setHtml({ code, lang, html: h }))
      // Without highlighting the plain text still reads fine.
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [code, lang]);

  if (html?.code === code && html.lang === lang)
    // Shiki escapes the code; the markup is only its own spans.
    return <div className={cn(PRE, '[&_pre]:whitespace-pre')} dangerouslySetInnerHTML={{ __html: html.html }} />;
  return <pre className={PRE}>{code}</pre>;
}
