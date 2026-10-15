import { useEffect, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { Button } from '../primitives/button';

type Props = {
  /** The text, or a function that fetches it when clicked. */
  text: string | (() => Promise<string>);
  label?: string;
  title?: string;
};

/** Copies to the clipboard and confirms with a check for a moment. */
export function CopyButton({ text, label, title }: Props) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (state === 'idle') return;
    const timer = setTimeout(() => setState('idle'), 1500);
    return () => clearTimeout(timer);
  }, [state]);

  const copy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      if (typeof text === 'string') {
        await navigator.clipboard.writeText(text);
      } else {
        // A pending ClipboardItem keeps the click's permission while the
        // text loads; Safari refuses writeText after an await.
        const blob = text().then((t) => new Blob([t], { type: 'text/plain' }));
        await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]);
      }
      setState('copied');
    } catch {
      setState('failed');
    }
  };

  const icon = state === 'copied' ? <Check className="text-success" /> : <Copy />;
  return (
    <Button
      variant="ghost"
      size={label ? 'sm' : 'icon'}
      className={label ? undefined : 'size-6'}
      title={title ?? 'Copy'}
      aria-label={label ?? title ?? 'Copy'}
      onClick={copy}
    >
      {icon}
      {label && (state === 'failed' ? 'Copy failed' : state === 'copied' ? 'Copied' : label)}
    </Button>
  );
}
