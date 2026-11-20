import {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {browseServerFilesystem} from '../../../../../../packages/client-core/src/library-management';
import type {FilesystemPage} from '../../../../../../packages/client-core/src/administration';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {appendExplorerPage, explorerEntries, toggleExplorerSelection, type ExplorerFolder, type ExplorerSelectionMode} from '../../../screens/settings-live/owner-libraries/folder-explorer-model';

export type FolderExplorerProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  api: LibraryContentApi;
  title: string;
  subtitle?: string;
  chosen: readonly ExplorerFolder[];
  onUse: (folders: readonly ExplorerFolder[]) => void;
  multiple?: boolean;
  selectionMode?: ExplorerSelectionMode;
  useLabel?: string;
  testID?: string;
}>;

export function useFolderExplorer({open, onOpenChange, api, chosen, onUse, multiple = true, selectionMode = 'folders'}: FolderExplorerProps) {
  const [path, setPath] = useState(''), [page, setPage] = useState<FilesystemPage>(), [error, setError] = useState<unknown>(),
    [loading, setLoading] = useState(false), [hidden, setHidden] = useState(false), [picked, setPicked] = useState<readonly ExplorerFolder[]>(chosen), [editing, setEditing] = useState(false), [typed, setTyped] = useState(''), [retry, setRetry] = useState(0);

  const chosenRef = useRef(chosen);
  useLayoutEffect(() => {chosenRef.current = chosen;}, [chosen]);
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
  const has = (folder: ExplorerFolder) => picked.some(value => value.path === folder.path);
  const add = (folder: ExplorerFolder) => setPicked(current => toggleExplorerSelection(current, folder, multiple));
  const here = page?.path ? {path: page.path, name: page.path.split(page.separator).filter(Boolean).pop() ?? page.path} : undefined;
  const choose = (next: string) => {setEditing(false); setPath(next);};
  const use = () => {if (open && picked.length) {onUse(picked); onOpenChange(false);}};

  return {path, page, error, loading, hidden, setHidden, picked, setPicked, editing, setEditing, typed, setTyped,
    setRetry, folders, has, add, here, choose, use, loadMore};
}
