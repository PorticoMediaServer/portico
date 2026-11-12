/** Moves between pages at this address without reloading the document (H14). The app registers
 * its router's history when it mounts; the Portico Account pages, rendered inside the app at
 * web.getportico.tv, then change pages in place, keeping the header, sidebar and backdrop
 * mounted. Without a registered app (the account pages opened alone for a captured link, or a
 * test) it falls back to an ordinary page load. Only same-origin paths go through here; another
 * origin is always a real navigation. */
export type InPageNavigate = (to: string, replace: boolean) => void;
let registered: InPageNavigate | undefined;

export function registerInPageNavigation(navigate: InPageNavigate): () => void {
  registered = navigate;
  return () => {if (registered === navigate) registered = undefined;};
}
export function inPageNavigationAvailable(): boolean {return registered !== undefined;}
/** The path `to` names on this address, read the way the browser will read it, or undefined
 * when it names another site. A browser takes `\` as `/` and drops tabs and line breaks, so
 * `/\evil.example` and `/\t/evil.example` are another site; `/.//evil.example` resolves to the
 * path `//evil.example`, which would be another site once handed back to the browser. */
export function pathOnThisAddress(to: string, origin = location.origin): string | undefined {
  if (!to.startsWith('/')) return undefined;
  let url: URL;
  try {url = new URL(to, origin);} catch {return undefined;}
  const path = url.pathname + url.search + url.hash;
  return url.origin === origin && !path.startsWith('//') ? path : undefined;
}
/** `to` is a path on this origin, such as `/account?tab=security` or `/servers#join`. */
export function goTo(to: string, options: Readonly<{replace?: boolean}> = {}): void {
  const path = pathOnThisAddress(to);
  if (path === undefined) throw new Error('Only a path on this address can be opened in place.');
  if (registered) {registered(path, options.replace === true); return;}
  if (options.replace) location.replace(path); else location.assign(path);
}
/** For an `<a href>` inside the app: open a same-origin path in place, leaving modified clicks
 * (new tab, new window) and other origins to the browser. */
export function inPageClick(event: Pick<MouseEvent, 'defaultPrevented' | 'button' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'preventDefault'>, href: string): void {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || !registered) return;
  const path = pathOnThisAddress(href);
  if (path === undefined) return;
  event.preventDefault();
  goTo(path);
}
