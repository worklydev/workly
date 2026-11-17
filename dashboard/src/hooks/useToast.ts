import { useMemo, type ReactNode } from 'react';
import { Toast } from '@base-ui/react/toast';

/** Shows a toast (see Toaster): `show({ title, description })`; errors stay until closed. */
export function useToast() {
  const { add } = Toast.useToastManager();
  return useMemo(
    () => ({
      show: (t: { title: ReactNode; description?: ReactNode }) => add(t),
      error: (title: ReactNode, description?: ReactNode) =>
        add({ title, description, type: 'error', priority: 'high', timeout: 0 }),
    }),
    [add],
  );
}
