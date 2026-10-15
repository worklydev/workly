import { useState } from 'react';
import { KeyRound } from 'lucide-react';
import { Button } from '../primitives/button';
import { Input } from '../primitives/input';
import { API_KEY_STORAGE, storedKey } from '../lib/apiKey';

export function ApiKeyPrompt({ retry }: { retry: () => void }) {
  const [key, setKey] = useState('');
  const hadKey = storedKey() !== undefined;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    localStorage.setItem(API_KEY_STORAGE, key.trim());
    retry();
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <form onSubmit={submit} className="flex w-full max-w-sm flex-col gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="text-xl">API key required</h1>
          <p className="text-muted-foreground">
            {hadKey
              ? 'The saved key was rejected. Enter the current one.'
              : 'This server requires an API key (WORKLY_API_KEY). It is kept in this browser only.'}
          </p>
        </div>
        <Input
          icon={<KeyRound />}
          type="password"
          autoFocus
          placeholder="API key"
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        <Button type="submit" variant="default" disabled={!key.trim()}>
          Continue
        </Button>
      </form>
    </div>
  );
}
