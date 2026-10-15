import { useState } from 'react';
import { Button } from '../primitives/button';

type Props = {
  children: React.ReactNode;
  confirmLabel: React.ReactNode;
  onConfirm: () => void;
  disabled?: boolean;
  /** "icon" for a compact row action; it widens to show confirmLabel. */
  size?: 'sm' | 'icon';
  title?: string;
};

/** A red ghost button that fills in and asks for a second click before acting. */
export function ConfirmButton({ children, confirmLabel, onConfirm, disabled, size = 'sm', title }: Props) {
  const [armed, setArmed] = useState(false);
  return (
    <Button
      size={armed ? 'sm' : size}
      title={armed ? undefined : title}
      variant={armed ? 'destructive' : 'ghost'}
      className={
        armed
          ? undefined
          : size === 'icon'
            ? 'text-muted-foreground hover:bg-destructive/12 hover:text-destructive'
            : 'text-destructive hover:bg-destructive/12'
      }
      disabled={disabled}
      onClick={() => {
        if (!armed) return setArmed(true);
        setArmed(false);
        onConfirm();
      }}
      onBlur={() => setArmed(false)}
    >
      {armed ? confirmLabel : children}
    </Button>
  );
}
