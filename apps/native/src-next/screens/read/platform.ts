import type {Library} from '../../../../../packages/client-core/src/index';
import type {NativeShellRoute} from '../../shell/routes';
import type {ShellDestination} from '../../shell/model';

/** Actual catalogue kinds; an audiobook is never a Read publication. */
export const isReadingLibraryKind = (kind: string): boolean => kind === 'book' || kind === 'comic';

/** Preserve the existing exclusion of the former unsupported UI spelling. */
/** A reading page's series or periodical id as the rebuilt server names it (`itm_…`), and the old server's 43-character
 * group ids: the URL-safe characters only, at most 128. It goes into a request path, so nothing else is let through. */
export const readingRouteId = (id: string): boolean => typeof id === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(id);
export const hiddenReadingLibraryOnTV = (kind: string): boolean => isReadingLibraryKind(kind) || kind === 'books';

/** Apply before invoking a registered renderer, including a restored origin.
 * Current authorized library facts decide; a stale library id cannot admit Read. */
export function nativeReadRouteAllowed(route: NativeShellRoute, libraries: readonly Library[], isTV: boolean, origin?: ShellDestination): boolean {
  if (!isTV) return true;
  if (route.kind === 'read' || route.kind === 'read-book' || route.kind === 'read-series' || route.kind === 'read-periodical' || route.kind === 'search-reading') return false;
  const allowedLibrary = (id: string) => {
    const library = libraries.find(candidate => candidate.id === id);
    return !!library && !hiddenReadingLibraryOnTV(library.kind);
  };
  // A normal shell destination can always leave a formerly admitted Read origin.
  if (route.kind === 'shell') return route.destination.kind !== 'library' || allowedLibrary(route.destination.libraryId);
  if (origin?.kind === 'library' && !allowedLibrary(origin.libraryId)) return false;
  const libraryId = route.kind === 'title' ? route.entry.libraryId : undefined;
  if (!libraryId) return route.kind !== 'title' || route.entry.kind !== 'book';
  return allowedLibrary(libraryId);
}
