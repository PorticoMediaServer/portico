/** Reveal before the browser tries to Tab into inert player controls. Focus is
 * requested after React commits visibility; an ordinary visible Tab stays native. */
export function revealVideoControlsOnTab(event: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'defaultPrevented' | 'preventDefault'> & Partial<Pick<KeyboardEvent, 'ctrlKey' | 'metaKey' | 'altKey'>>,
  visible: boolean, revealAndFocus: (backwards: boolean) => void): boolean {
  if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.key !== 'Tab' || visible) return false;
  event.preventDefault();
  revealAndFocus(event.shiftKey);
  return true;
}

/** Keyboard focus holds the chrome; pointer focus can be blurred before hiding. */
export function videoControlsMayHide(chrome: Pick<HTMLElement, 'contains'> | null, active: Node | null): boolean {
  if (!active || !chrome?.contains(active)) return true;
  try { return !(active as Element).matches(':focus-visible'); }
  // Older selector engines cannot prove pointer focus: retain accessible controls.
  catch { return false; }
}
