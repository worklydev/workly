import { useState, type ReactNode } from 'react';
import { MoreHorizontal } from 'lucide-react';
import { cn } from 'cn';
import { Button } from '../primitives/button';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../primitives/menu';

/**
 * The "⋯" at the end of a page header's actions, for the destructive ones:
 * they stay one click away without crowding the actions people use daily.
 */
export function MoreActions({ children, label = 'More actions' }: { children: ReactNode; label?: string }) {
  return (
    <Menu>
      <MenuTrigger
        render={<Button variant="ghost" size="icon" aria-label={label} title={label} />}
        className="data-popup-open:bg-muted data-popup-open:text-foreground"
      >
        <MoreHorizontal />
      </MenuTrigger>
      <MenuContent>{children}</MenuContent>
    </Menu>
  );
}

/**
 * A destructive item that asks twice, like ConfirmButton: the first click turns
 * it red with `confirmLabel` and keeps the menu open, the second acts. When it
 * can't act, it stays listed, greyed, with the reason underneath.
 */
export function ConfirmItem({
  icon,
  children,
  confirmLabel,
  onConfirm,
  disabledReason,
  title,
}: {
  icon?: ReactNode;
  children: ReactNode;
  confirmLabel: ReactNode;
  onConfirm: () => void;
  disabledReason?: string;
  title?: string;
}) {
  const [armed, setArmed] = useState(false);
  const disabled = disabledReason !== undefined;
  return (
    <MenuItem
      disabled={disabled}
      closeOnClick={armed}
      title={armed ? undefined : title}
      className={cn(
        'items-start text-destructive data-highlighted:bg-destructive/12',
        armed && 'bg-destructive/12',
        disabled && 'text-faint',
      )}
      onClick={() => {
        if (!armed) return setArmed(true);
        setArmed(false);
        onConfirm();
      }}
    >
      <span className="mt-0.5 flex">{icon}</span>
      <span className="flex flex-col">
        <span className={armed ? 'font-medium' : undefined}>{armed ? confirmLabel : children}</span>
        {disabled && <span className="text-xs text-faint">{disabledReason}</span>}
      </span>
    </MenuItem>
  );
}
