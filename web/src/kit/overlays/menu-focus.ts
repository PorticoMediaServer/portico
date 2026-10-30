export function menuInitialFocus(root: HTMLElement): HTMLElement {
  return root.querySelector<HTMLElement>('[data-menu-item]:not(:disabled)') ?? root;
}

/** A child picker can replace the currently focused row. Recover only that lost
 * focus; an outside click or another overlay keeps its own focus. */
export function recoverMenuChildFocus(root: HTMLElement, previous: HTMLElement | null) {
  if (!previous || root.contains(previous) || root.ownerDocument.activeElement !== root.ownerDocument.body) return;
  const target = root.querySelector<HTMLElement>('h1,h2,h3,[data-menu-heading]') ?? menuInitialFocus(root);
  if (!target.hasAttribute('tabindex')) target.tabIndex = -1;
  target.focus({preventScroll: true});
}

/** Menu arrows move focus only; native button activation owns selection. */
export function navigateMenuArrow(event: {key: string; defaultPrevented: boolean; altKey: boolean; ctrlKey: boolean; metaKey: boolean; currentTarget: HTMLElement; preventDefault(): void}): boolean {
  if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return false;
  const rows = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-menu-item]:not(:disabled)'));
  if (!rows.length) return false;
  const current = rows.indexOf(event.currentTarget.ownerDocument.activeElement as HTMLButtonElement);
  const next = event.key === 'ArrowDown' ? rows[(current + 1) % rows.length]
    : event.key === 'ArrowUp' ? rows[(current - 1 + rows.length) % rows.length]
    : event.key === 'Home' ? rows[0] : event.key === 'End' ? rows.at(-1) : undefined;
  if (!next) return false;
  event.preventDefault(); next.focus({preventScroll: true}); return true;
}
