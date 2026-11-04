import {LibraryManagementService, type LibraryManagementSnapshot} from '../../../../../packages/client-core/src/library-management';
import type {LibraryContentApi, ContentScope} from '../../../../../packages/client-core/src/library-content';
import type {ViewerQueries} from '../../../../../packages/client-core/src/query/viewer-queries';

/** states.html #15: "Titles appear here as they're found". Only the owner's complete one-job
 * history, whose one job is this library's scan and is still queued or running, establishes a
 * first scan (the condition web's LibraryEmpty uses). */
export function isFirstScan(snapshot: Pick<LibraryManagementSnapshot, 'selected' | 'jobs'>, libraryId: string): boolean {
  const {selected, jobs} = snapshot, library = selected.data?.library, history = jobs.data;
  return selected.phase === 'ready' && jobs.phase === 'ready' && library?.id === libraryId &&
    !!library.lastScan && ['queued', 'running'].includes(library.lastScan.status) && !!history &&
    !history.nextCursor && history.items.length === 1 && history.items[0].id === library.lastScan.id;
}

/** What the library screen is given: the live first-scan check for one library, and the owner's
 * way to server activity. Absent for members, who get neither the check nor an action. */
export type NativeLibraryFirstScanPort = Readonly<{
  /** Sample once for one library's empty page; `onChange` follows the answer until the returned stop is called. */
  observe: (libraryId: string, onChange: (firstScan: boolean) => void) => () => void;
  /** Open the server activity the owner's Dashboard (TV: Server settings) shows. */
  openActivity: () => void;
}>;

/** The owner's first-scan observer: one management service per empty page, read once and then
 * followed, disposed with the page. A failed read is simply "not a first scan". */
export function observeFirstScan(input: Readonly<{api: LibraryContentApi; scope: ContentScope; queries?: ViewerQueries}>, libraryId: string, onChange: (firstScan: boolean) => void): () => void {
  let current = true;
  const service = new LibraryManagementService(input);
  const update = () => {if (current) onChange(isFirstScan(service.getSnapshot(), libraryId));};
  const off = service.subscribe(update);
  void service.selectLibrary(libraryId).then(update).catch(() => {if (current) onChange(false);});
  return () => {current = false; off(); service.dispose();};
}
