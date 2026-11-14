// Syntax highlighting for code panels, loaded only when one asks for it, so
// the dashboard's bundle doesn't carry Shiki. The theme is CSS variables
// mapped to the dashboard's tokens in index.css (as on workly.run), so
// highlighted code follows light and dark like everything else.
import { createCssVariablesTheme, createHighlighterCore, type HighlighterCore } from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';

export type CodeLang = 'python' | 'typescript' | 'bash' | 'json' | 'yaml';

const LANGS: Record<CodeLang, () => Promise<unknown>> = {
  python: () => import('shiki/langs/python.mjs'),
  typescript: () => import('shiki/langs/typescript.mjs'),
  bash: () => import('shiki/langs/bash.mjs'),
  json: () => import('shiki/langs/json.mjs'),
  yaml: () => import('shiki/langs/yaml.mjs'),
};

const theme = createCssVariablesTheme({ name: 'workly', variablePrefix: '--shiki-' });

let highlighter: Promise<HighlighterCore> | undefined;

/** The code as HTML: spans coloured by CSS variables, inside a bare <pre>. */
export async function highlight(code: string, lang: CodeLang): Promise<string> {
  highlighter ??= createHighlighterCore({ themes: [theme], langs: [], engine: createJavaScriptRegexEngine() });
  const h = await highlighter;
  if (!h.getLoadedLanguages().includes(lang)) {
    const mod = (await LANGS[lang]()) as { default: Parameters<HighlighterCore['loadLanguage']>[0] };
    await h.loadLanguage(mod.default);
  }
  return h.codeToHtml(code, {
    lang,
    theme: 'workly',
    transformers: [
      {
        pre(node) {
          // The panel styles the block; Shiki's inline colours would fight the tokens.
          delete node.properties.style;
          delete node.properties.tabindex;
          node.properties.class = 'shiki';
        },
      },
    ],
  });
}
