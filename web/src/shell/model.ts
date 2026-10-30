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
/** Title and person pages stay inside the library that opened them; a bare direct visit claims none. */
export function activeLibraryId(path: string, remembered: string | undefined): string | undefined {
  if (path.startsWith('/library/')) {
    try { return decodeURIComponent(path.slice('/library/'.length).split('/')[0]) || undefined; } catch { return undefined; }
  }
  return remembered && (path.startsWith('/item/') || path.startsWith('/person/')) ? remembered : undefined;
}
export function shellGutter(width: number): number { return width < 1024 ? 20 : Math.min(64, 32 + (width - 1024) / 13); }

export function browserOwnsBackTransition(event: {hasUAVisualTransition?: boolean}, environment: {iphoneSafari: boolean; traversal: boolean}): boolean {
  return event.hasUAVisualTransition === true || environment.iphoneSafari && environment.traversal;
}
