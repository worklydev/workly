import { Menu as MenuPrimitive } from '@base-ui/react/menu';
import { cn } from 'cn';

const Menu = MenuPrimitive.Root;
const MenuTrigger = MenuPrimitive.Trigger;

// Same surface as popovers: popover fill, input hairline, the one shadow overlays get.
function MenuContent({
  className,
  side = 'bottom',
  align = 'end',
  sideOffset = 6,
  ...props
}: MenuPrimitive.Popup.Props & Pick<MenuPrimitive.Positioner.Props, 'side' | 'align' | 'sideOffset'>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Positioner side={side} align={align} sideOffset={sideOffset} className="isolate z-50">
        <MenuPrimitive.Popup
          className={cn(
            'flex min-w-48 origin-(--transform-origin) flex-col rounded-md border border-input bg-popover p-1 text-sm text-popover-foreground shadow-lg shadow-black/20 outline-none duration-100 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95',
            className,
          )}
          {...props}
        />
      </MenuPrimitive.Positioner>
    </MenuPrimitive.Portal>
  );
}

function MenuItem({ className, ...props }: MenuPrimitive.Item.Props) {
  return (
    <MenuPrimitive.Item
      className={cn(
        'flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 outline-none select-none data-disabled:cursor-not-allowed data-disabled:text-faint data-highlighted:bg-muted [&_svg]:size-4 [&_svg]:shrink-0',
        className,
      )}
      {...props}
    />
  );
}

export { Menu, MenuTrigger, MenuContent, MenuItem };
