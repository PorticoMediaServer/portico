/** The library the shell highlights away from a library's own address, kept in memory only:
 * a title page claims its own library once its identity arrives, and a person page keeps the
 * last library claimed in this page session (none on a direct visit). */
export type TitleLibraryClaim = Readonly<{itemId: string; libraryId: string}>;
type TitleLibraryState = Readonly<{title?: TitleLibraryClaim; last?: string}>;

let state: TitleLibraryState = Object.freeze({});
const listeners = new Set<() => void>();
const publish = (next: TitleLibraryState) => {state = Object.freeze(next);for (const listener of [...listeners]) listener();};

export function claimTitleLibrary(itemId: string, libraryId: string): void {
  if (state.title?.itemId === itemId && state.title.libraryId === libraryId && state.last === libraryId) return;
  publish({title: Object.freeze({itemId, libraryId}), last: libraryId});
}
export function noteLibraryVisit(libraryId: string): void {
  if (state.last !== libraryId) publish({...state, last: libraryId});
}
/** A sign-out or another profile or server starts with no library claimed: the next viewer never sees the last
 * viewer's library highlighted. */
export function forgetTitleLibrary(): void {
  if (state.title || state.last !== undefined) publish({});
}
export const titleLibrary = Object.freeze({
  subscribe(listener: () => void) {listeners.add(listener);return () => {listeners.delete(listener);};},
  getSnapshot: (): TitleLibraryState => state,
});
