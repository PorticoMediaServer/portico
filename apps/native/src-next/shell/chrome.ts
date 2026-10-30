/** A destination that draws its own full-height surface (a pushed Settings page)
 * hides the shell's navigation while it is open, without changing the route.
 * Owners register by name so a screen that unmounts cannot strand the chrome. */
const hiddenBy = new Set<string>();
const listeners = new Set<() => void>();
const publish = () => {for (const listener of [...listeners]) listener();};

export function setNativeScreenChromeHidden(owner: string, hidden: boolean): void {
  const before = hiddenBy.has(owner);
  if (hidden === before) return;
  if (hidden) hiddenBy.add(owner); else hiddenBy.delete(owner);
  publish();
}

export function nativeScreenChromeHidden(): boolean {
  return hiddenBy.size > 0;
}

export function subscribeNativeScreenChrome(listener: () => void): () => void {
  listeners.add(listener);
  return () => {listeners.delete(listener);};
}
