import {useOverlayPortalTarget} from './portal-target';
import {Fragment, createContext, forwardRef, useCallback, useContext, useId, useEffect, useLayoutEffect, useMemo, useRef, type ButtonHTMLAttributes, type KeyboardEvent, type ReactNode} from 'react';
import {Popover as RadixPopover} from 'radix-ui';
import {Icon, type IconName} from '../Icon';
import {BottomSheet} from './BottomSheet';
import {classes, focusElement, focusable, useDismissReason, useExclusiveAnchor, useInDialog, useOverlayKeys, usePhone, usePresence, useSurfaceFocus, useSurfaceMotion} from './shared';
import type {AnchoredOverlayProps, OverlayChange, OverlayStyle} from './types';
import {menuInitialFocus, navigateMenuArrow, recoverMenuChildFocus} from './menu-focus';
import styles from './overlays.module.css';

/** A caller-supplied virtual anchor, queried when Radix positions an external
 * anchor surface. It requires `anchorRef` and cannot be combined with a trigger;
 * the real `anchorRef` still owns ARIA, outside interaction and return focus. */
export type PopoverGeometry = Readonly<{getBoundingClientRect: () => DOMRect}>;

export type PopoverProps = AnchoredOverlayProps & {role?: 'dialog' | 'menu'; onContentKeyDown?: (event: KeyboardEvent<HTMLDivElement>) => void; geometry?: PopoverGeometry};

export function Popover({open, onOpenChange, title, children, description, trigger, anchorRef, geometry, modal = false, placement = 'bottom-end', sideOffset = 8, alignOffset = 0, width = 300, initialFocusRef, returnFocusRef, accessibilityLabel, className, testID, role = 'dialog', onContentKeyDown}: PopoverProps) {
  const portalTarget = useOverlayPortalTarget();
  const triggerElement = useRef<HTMLButtonElement | null>(null);
  const id = useId(), reason = useDismissReason(), focus = useSurfaceFocus(open, initialFocusRef, returnFocusRef ?? anchorRef ?? triggerElement);
  const withinDialog = useInDialog(open, anchorRef ?? triggerElement);
  const present = usePresence(open, 160), motionRef = useSurfaceMotion(open, 'menu');
  const {contentRef: focusContentRef} = focus;
  const attachContent = useCallback((element: HTMLDivElement | null) => {motionRef(element);focusContentRef(element);}, [motionRef, focusContentRef]);
  const anchor = useMemo(() => ({current: {
    getBoundingClientRect: () => geometry?.getBoundingClientRect() ?? anchorRef?.current?.getBoundingClientRect() ?? new DOMRect(),
  }}), [anchorRef, geometry]);
  useOverlayKeys(open, onOpenChange); const superseded = useExclusiveAnchor(open, onOpenChange);
  if (superseded) focus.suppressRestore.current = true;
  useLayoutEffect(() => {
    const element = anchorRef?.current;
    if (trigger || !element) return;
    const names = ['aria-haspopup', 'aria-expanded', 'aria-controls'] as const, original = names.map(name => element.getAttribute(name));
    element.setAttribute('aria-haspopup', role); element.setAttribute('aria-expanded', String(open));
    if (open) element.setAttribute('aria-controls', `${id}-content`); else element.removeAttribute('aria-controls');
    return () => names.forEach((name, index) => { const previous = original[index]; if (previous === null || previous === undefined) element.removeAttribute(name); else element.setAttribute(name, previous); });
  }, [trigger, anchorRef, open, role, id]);
  if (geometry && (trigger || !anchorRef)) throw new TypeError('Popover geometry requires an anchorRef and cannot be combined with a trigger.');
  if (!trigger && !anchorRef) throw new TypeError('Popover requires a trigger or an anchor ref.');
  return <RadixPopover.Root open={open} onOpenChange={value => { if (value === open) return; onOpenChange(value, reason.current); reason.current = 'action'; }} modal={modal}>
    {trigger ? <RadixPopover.Trigger asChild ref={triggerElement} aria-haspopup={role} aria-controls={open ? `${id}-content` : undefined}>{trigger}</RadixPopover.Trigger> : <RadixPopover.Anchor virtualRef={anchor} />}
    {/* Exit presence must not leave the positioning wrapper as a pointer target. */}
    {present && !superseded && <RadixPopover.Portal forceMount container={portalTarget}><div style={{pointerEvents: 'none'}}><RadixPopover.Content forceMount ref={attachContent}
      side={placement.startsWith('top') ? 'top' : 'bottom'} align={placement.endsWith('start') ? 'start' : 'end'} sideOffset={sideOffset} alignOffset={alignOffset} collisionPadding={8}
      id={`${id}-content`} className={classes(styles.popover, className)} style={{'--overlay-width': `${width}px`, pointerEvents: open ? 'auto' : 'none'} as OverlayStyle} role={role} aria-modal={modal && role === 'dialog' && open || undefined}
      aria-label={accessibilityLabel} aria-labelledby={accessibilityLabel ? undefined : `${id}-title`} aria-describedby={description === undefined ? undefined : `${id}-description`}
      data-state={open ? 'open' : 'closed'} data-within-dialog={withinDialog || undefined} data-testid={testID}
      onOpenAutoFocus={focus.onOpenAutoFocus} onCloseAutoFocus={focus.onCloseAutoFocus}
      onEscapeKeyDown={() => { reason.current = 'escape'; }}
      onInteractOutside={event => { if (!open || focus.suppressRestore.current || event.target instanceof Node && anchorRef?.current?.contains(event.target)) { event.preventDefault(); return; } reason.current = 'outside'; if (!modal) focus.suppressRestore.current = true; }}
      onKeyDown={event => { onContentKeyDown?.(event); if (event.key === 'Tab' && role === 'menu' && !event.defaultPrevented) {
        event.preventDefault(); focus.suppressRestore.current = true;
        const origin = returnFocusRef?.current ?? anchorRef?.current ?? triggerElement.current;
        const candidates = focusable(portalTarget ?? document.body).filter(item => !event.currentTarget.contains(item));
        const position = origin ? candidates.indexOf(origin) : -1;
        const next = position < 0 ? candidates[0] : candidates[(position + (event.shiftKey ? -1 : 1) + candidates.length) % candidates.length];
        onOpenChange(false, 'action'); focusElement(next);
      } }}>
      <span id={`${id}-title`} className={styles.visuallyHidden}>{title}</span>
      {description !== undefined && <div id={`${id}-description`} className={styles.popoverDescription}>{description}</div>}
      {children}
    </RadixPopover.Content></div></RadixPopover.Portal>}
  </RadixPopover.Root>;
}

type RowExtras = {icon?: IconName; description?: ReactNode; selected?: boolean; destructive?: boolean};
export type MenuRowProps = ButtonHTMLAttributes<HTMLButtonElement> & RowExtras;
export const MenuRow = forwardRef<HTMLButtonElement, MenuRowProps>(function MenuRow({icon, description, selected, destructive = false, children, className, type = 'button', ...props}, ref) {
  return <button {...props} ref={ref} type={type} className={classes(styles.menuRow, className)} data-selected={selected || undefined} data-destructive={destructive || undefined} aria-pressed={props.role?.startsWith('menuitem') || props.role === 'radio' ? undefined : selected}>
    {icon && <span className={styles.menuIcon}><Icon name={icon} size={18} /></span>}
    <span className={styles.menuLabel}>{children}</span>
    {description !== undefined && <span className={styles.menuDescription}>{description}</span>}
    {selected && <Icon name="i-check" size={18} className={styles.menuCheck} />}
  </button>;
});

const MenuContext = createContext<{change: OverlayChange} | null>(null);
export type MenuItemProps = Omit<MenuRowProps, 'onSelect'> & {onSelect?: (event: Event) => void};
export const MenuItem = forwardRef<HTMLButtonElement, MenuItemProps>(function MenuItem({onSelect, onClick, selected, ...props}, ref) {
  const context = useContext(MenuContext);
  return <MenuRow {...props} ref={ref} selected={selected} role={selected === undefined ? 'menuitem' : 'menuitemradio'} aria-checked={selected} data-menu-item="" tabIndex={-1} onClick={event => {
    onClick?.(event); if (event.defaultPrevented) return;
    const selection = new Event('select', {cancelable: true}); onSelect?.(selection);
    if (!selection.defaultPrevented) context?.change(false, 'action');
  }} onPointerMove={event => { props.onPointerMove?.(event); if (event.pointerType === 'mouse' && !event.currentTarget.disabled) event.currentTarget.focus({preventScroll: true}); }} />;
});

export type MenuGroupProps = {label?: ReactNode; ownerOnly?: boolean; children: ReactNode; className?: string};
export function MenuGroup({label, ownerOnly = false, children, className}: MenuGroupProps) {
  const id = useId();
  return <div className={classes(styles.menuGroup, ownerOnly && styles.ownerGroup, className)} role="group" aria-labelledby={label === undefined ? undefined : id} data-owner-only={ownerOnly || undefined}>
    {label !== undefined && <div id={id} className={styles.menuGroupLabel}>{label}</div>}<div className={styles.menuGroupRows}>{children}</div>
  </div>;
}
export function MenuSeparator() { return <div className={styles.menuSeparator} role="separator" />; }

export type MenuEntry = Readonly<{id: string; label: ReactNode; icon?: IconName; description?: ReactNode; disabled?: boolean; selected?: boolean; destructive?: boolean; onSelect: () => void} | {id: string; separator: true} | {id: string; label: ReactNode; section: true; ownerOnly?: boolean}>;
export type MenuProps = Omit<AnchoredOverlayProps, 'children'> & {items?: readonly MenuEntry[]; children?: ReactNode; presentation?: 'anchored' | 'responsive'};

function EntryGroups({items, phone}: {items: readonly MenuEntry[]; phone: boolean}) {
  type Action = Exclude<MenuEntry, {separator: true} | {section: true}>;
  const groups: {id: string; label?: ReactNode; ownerOnly?: boolean; separator?: boolean; items: Action[]}[] = [{id: 'start', items: []}];
  for (const item of items) {
    if ('separator' in item) groups.push({id: item.id, separator: true, items: []});
    else if ('section' in item) groups.push({id: item.id, label: item.label, ownerOnly: item.ownerOnly, items: []});
    else groups.at(-1)!.items.push(item);
  }
  return <>{groups.filter(group => group.items.length || group.label !== undefined).map(group => <Fragment key={group.id}>
    {!phone && group.separator && <MenuSeparator />}<MenuGroup label={group.label} ownerOnly={group.ownerOnly}>{group.items.map(item => <MenuItem key={item.id} icon={item.icon} description={item.description} disabled={item.disabled} selected={item.selected} destructive={item.destructive} onSelect={item.onSelect}>{item.label}</MenuItem>)}</MenuGroup>
  </Fragment>)}</>;
}

export function Menu({items, children, open, onOpenChange, title, presentation = 'responsive', ...props}: MenuProps) {
  const phone = usePhone(), typeahead = useRef({text: '', time: 0}), initial = useRef<HTMLElement | null>(null);
  const observer = useRef<MutationObserver | null>(null), lastFocused = useRef<HTMLElement | null>(null);
  // The portal may mount after this owner's layout effect. Attach to its actual
  // content node so child-only view swaps also retain a focus recovery owner.
  const attachContent = useCallback((element: HTMLDivElement | null) => {
    observer.current?.disconnect();observer.current = null;
    initial.current = element ? menuInitialFocus(element) : null;
    if (!open || !element) {lastFocused.current = null;return;}
    const recover = () => recoverMenuChildFocus(element, lastFocused.current);
    observer.current = new MutationObserver(recover);
    observer.current.observe(element, {childList: true, subtree: true});
    recover();
  }, [open]);
  const sheet = phone && presentation === 'responsive';
  useExclusiveAnchor(sheet && open, onOpenChange);
  const keydown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || navigateMenuArrow(event)) return;
    const rows = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-menu-item]:not(:disabled)'));
    if (!rows.length) return;
    const current = rows.indexOf(document.activeElement as HTMLButtonElement);
    let next: HTMLButtonElement | undefined;
    if (event.key.length === 1 && event.key !== ' ') {
      const now = performance.now(), previous = now - typeahead.current.time < 700 ? typeahead.current.text : '';
      typeahead.current = {text: previous + event.key.toLocaleLowerCase(), time: now};
      const query = typeahead.current.text, repeated = [...query].every(char => char === query[0]);
      const sequence = [...rows.slice(current + 1), ...rows.slice(0, current + 1)];
      next = sequence.find(row => row.textContent?.trim().toLocaleLowerCase().startsWith(repeated ? query[0]! : query));
    }
    if (next) { event.preventDefault(); focusElement(next); }
  };
  const content = <MenuContext.Provider value={{change: onOpenChange}}><div tabIndex={-1} ref={attachContent} onFocusCapture={event => {lastFocused.current = event.target as HTMLElement;}} onKeyDown={keydown} className={styles.menuItems}>
    {items && <EntryGroups items={items} phone={sheet} />}
    {children}
  </div></MenuContext.Provider>;
  if (sheet) return <BottomSheet open={open} onOpenChange={onOpenChange} trigger={props.trigger} title={title} description={props.description} className={classes(styles.phoneMenu, props.className)} fitContent initialFocusRef={props.initialFocusRef ?? initial} returnFocusRef={props.returnFocusRef ?? props.anchorRef} accessibilityLabel={props.accessibilityLabel} testID={props.testID}>{content}</BottomSheet>;
  return <Popover {...props} open={open} onOpenChange={onOpenChange} title={title} role="menu" className={classes(styles.menu, props.className)} initialFocusRef={props.initialFocusRef ?? initial}>{content}</Popover>;
}
