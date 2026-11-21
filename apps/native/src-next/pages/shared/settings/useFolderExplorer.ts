import {useEffect, useMemo, useRef, useState} from 'react';
import {browseServerFilesystem} from '../../../../../../packages/client-core/src/library-management';
import type {FilesystemPage} from '../../../../../../packages/client-core/src/administration';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {appendExplorerPage, explorerEntries, toggleExplorerSelection, type ExplorerFolder, type ExplorerSelectionMode} from '../../../screens/settings-live/owner-libraries/folder-explorer-model';

/** The server's own disks, a page at a time: where you are, the places at the top, the folders (and, for a restore, the
 * readable files) here, which are chosen, and the typed path. Moved from the old FolderExplorer as it was. */
export function useFolderExplorer({open, api, chosen, multiple, selectionMode}: {open: boolean; api: LibraryContentApi; chosen: readonly ExplorerFolder[];
  multiple: boolean; selectionMode: ExplorerSelectionMode}) {
  const [path, setPath] = useState(''), [page, setPage] = useState<FilesystemPage>(), [error, setError] = useState<unknown>(),
    [loading, setLoading] = useState(false), [hidden, setHidden] = useState(false), [picked, setPicked] = useState<readonly ExplorerFolder[]>(chosen), [editing, setEditing] = useState(false), [typed, setTyped] = useState(''), [retry, setRetry] = useState(0);
  const chosenRef = useRef(chosen); chosenRef.current = chosen;
  const request = useRef<AbortController | undefined>(undefined), pending = useRef(false);
  useEffect(() => {if (open) {setPicked(chosenRef.current); setPath(''); setError(undefined);}}, [open]);
  useEffect(() => {
    if (!open) return;
    const owned = new AbortController(); request.current = owned; pending.current = true;
    setLoading(true); setError(undefined); setPage(undefined);
    browseServerFilesystem(api, {path: path || undefined, showHidden: hidden, includeFiles: selectionMode === 'file-or-folder', limit: 200}, owned.signal)
      .then(value => {if (!owned.signal.aborted && request.current === owned) {setPage(value); setTyped(value.path);}})
      .catch(reason => {if (!owned.signal.aborted && request.current === owned) setError(reason);})
      .finally(() => {if (!owned.signal.aborted && request.current === owned) {pending.current = false; setLoading(false);}});
    return () => {owned.abort(); if (request.current === owned) request.current = undefined;};
  }, [open, path, hidden, api, retry, selectionMode]);
  const loadMore = () => {
    const owned = request.current, base = page, cursor = base?.nextCursor;
    if (!owned || owned.signal.aborted || pending.current || !base || !cursor) return;
    pending.current = true; setLoading(true); setError(undefined);
    browseServerFilesystem(api, {path: path || undefined, showHidden: hidden, includeFiles: selectionMode === 'file-or-folder', cursor, limit: 200}, owned.signal)
      .then(value => {
        if (owned.signal.aborted || request.current !== owned) return;
        const next = appendExplorerPage(base, value);
        setPage(current => current === base ? next : current);
      })
      .catch(reason => {if (!owned.signal.aborted && request.current === owned) setError(reason);})
      .finally(() => {if (!owned.signal.aborted && request.current === owned) {pending.current = false; setLoading(false);}});
  };
  const folders = useMemo(() => explorerEntries(page, selectionMode), [page, selectionMode]);
  const choose = (next: string) => {setEditing(false); setPath(next);};
  return {page, error, loading, hidden, setHidden, picked, editing, typed, setTyped, folders, loadMore, choose,
    here: page?.path ? {path: page.path, name: page.path.split(page.separator).filter(Boolean).pop() ?? page.path} : undefined,
    has: (folder: ExplorerFolder) => picked.some(value => value.path === folder.path),
    add: (folder: ExplorerFolder) => setPicked(current => toggleExplorerSelection(current, folder, multiple)),
    unpick: (path: string) => setPicked(current => current.filter(value => value.path !== path)),
    retry: () => setRetry(value => value + 1),
    toggleEditing: () => {setEditing(value => !value); setTyped(page?.path ?? '');},
    startEditing: () => {setEditing(true); setTyped(page?.path ?? '');}};
}
