import type {Library} from '../../../../../packages/client-core/src/index';
import type {NativeShellRoute} from '../../shell/routes';
import type {ShellDestination} from '../../shell/model';

/** Actual catalogue kinds; an audiobook is never a Read publication. */
export const isReadingLibraryKind = (kind: string): boolean => kind === 'book' || kind === 'comic';

/** Preserve the existing exclusion of the former unsupported UI spelling. */
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
