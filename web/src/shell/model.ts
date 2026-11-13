import type {Library, Viewer} from '@core/index.ts';

export type ShellScope = Pick<Viewer, 'serverId' | 'accountId' | 'profileId' | 'authority'>;
export type LibraryMemoryStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export const desktopLibraryLimit = 6;

/** The input is the server's current entitled list, including its pin order. */
export function desktopLibraries(libraries: readonly Library[]) {
  return {visible: libraries.slice(0, desktopLibraryLimit), overflow: libraries.slice(desktopLibraryLimit)};
}
export function libraryMemoryKey(scope: ShellScope): string {
  return 'portico.next.last-library.' + JSON.stringify([scope.serverId, scope.authority, scope.accountId, scope.profileId]);
}
/** No first-library fallback: first use and removed access open the overview. */
export function rememberedLibrary(storage: LibraryMemoryStorage | undefined, scope: ShellScope, libraries: readonly Library[]): string | undefined {
  try {
    const key = libraryMemoryKey(scope), id = storage?.getItem(key);
    if (id && libraries.some(library => library.id === id)) return id;
    if (id) storage?.removeItem(key);
  } catch { /* Storage refusal does not change the entitled destinations. */ }
  return undefined;
}
export function rememberLibrary(storage: LibraryMemoryStorage | undefined, scope: ShellScope, libraries: readonly Library[], id: string): void {
  if (!libraries.some(library => library.id === id)) return;
  try { storage?.setItem(libraryMemoryKey(scope), id); } catch { /* This browser can still navigate without persistence. */ }
}
export function showOwnerActivity(role: string, desktop: boolean): boolean { return role === 'owner' && desktop; }
export function isLibraryLocation(path: string): boolean { return path === '/libraries' || path.startsWith('/library/'); }
/** A title page marks its own library once its identity arrives; a person page keeps the last
 * library claimed in this page session, so a direct visit claims none. */
export function activeLibraryId(path: string, opened: Readonly<{title?: Readonly<{itemId: string; libraryId: string}>; last?: string}> = {}): string | undefined {
  const segment = (prefix: string) => { try { return decodeURIComponent(path.slice(prefix.length).split('/')[0]) || undefined; } catch { return undefined; } };
  if (path.startsWith('/library/')) return segment('/library/');
  if (path.startsWith('/item/')) { const itemId = segment('/item/'); return itemId && opened.title?.itemId === itemId ? opened.title.libraryId : undefined; }
  return path.startsWith('/person/') ? opened.last : undefined;
}
export function shellGutter(width: number): number { return width < 1024 ? 20 : Math.min(64, 32 + (width - 1024) / 13); }

export function browserOwnsBackTransition(event: {hasUAVisualTransition?: boolean}, environment: {iphoneSafari: boolean; traversal: boolean}): boolean {
  return event.hasUAVisualTransition === true || environment.iphoneSafari && environment.traversal;
}
