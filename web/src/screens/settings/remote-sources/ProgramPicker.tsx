import {useCallback, useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {browseServerFilesystem} from '@core/library-management.ts';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './remote-sources.module.css';

const t = defaultI18n.t;

/** Chooses the rclone program file for a mounted source (remote-sources.html#2d
 * file mode). The server approves it by running it with version. */
export function ProgramPicker({open, api, startAt, onOpenChange, onChoose}: Readonly<{
  open: boolean; api: LibraryContentApi; startAt: string; onOpenChange: (open: boolean) => void; onChoose: (path: string) => void;
}>) {
  const [path, setPath] = useState('');
  const [entries, setEntries] = useState<readonly {path: string; name: string; directory: boolean; readable: boolean}[]>([]);
  const [typed, setTyped] = useState(startAt);
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const request = useRef<AbortController | undefined>(undefined);
  const read = useCallback(async (target?: string) => {
    request.current?.abort();
    const active = new AbortController();
    request.current = active;
    setBusy(true); setFailed(false);
    try {
      const page = await browseServerFilesystem(api, {...(target ? {path: target} : {}), includeFiles: true, limit: 200}, active.signal);
      if (active.signal.aborted) return;
      setPath(page.path);
      setTyped(page.path);
      setEntries(page.entries.map(entry => ({path: entry.path, name: entry.name, directory: entry.kind === 'directory', readable: entry.readable})));
    } catch {if (!active.signal.aborted) setFailed(true);}
    finally {if (!active.signal.aborted) setBusy(false);}
  }, [api]);
  useEffect(() => {
    if (open) {setEntries([]);setFailed(false);setTyped(startAt);void read(startAt || undefined);}
    return () => {request.current?.abort();request.current = undefined;};
  }, [open, read, startAt]);
  return <Dialog open={open} onOpenChange={onOpenChange} width={720} title={t('next.remoteSources.chooseProgram')} description={t('next.remoteSources.chooseProgramHelp')}
    footer={<><Button size="compact" variant="glass" onClick={() => onOpenChange(false)}>{t('next.remoteSources.cancel')}</Button>
      <Button size="compact" variant="primary" disabled={busy || !typed.trim()} onClick={() => {if (typed.trim()) {onChoose(typed.trim());onOpenChange(false);}}}>{t('next.remoteSources.useProgram')}</Button></>}>
    <div className={styles.explorer}>
      <div className={styles.explorerPath}>
        <TextField label={t('next.remoteSources.programPath')} value={typed} disabled={busy} autoCapitalize="off" spellCheck={false}
          className={styles.monoInput} onChange={event => setTyped(event.currentTarget.value)}
          onKeyDown={event => {if (event.key === 'Enter' && !event.nativeEvent.isComposing) {event.preventDefault(); void read(typed.trim() || undefined);}}}/>
        <Button size="small" variant="glass" disabled={busy} onClick={() => void read(typed.trim() || undefined)}>{t('next.remoteSources.go')}</Button>
      </div>
      {failed && <p className={styles.error} role="alert">{t('next.remoteSources.programListFailed')}</p>}
      {busy && <p className={styles.explorerNote} role="status">{t('next.remoteSources.folderListing')}</p>}
      {!busy && !failed && <ul className={styles.folderList}>
        {entries.map(entry => <li key={entry.path} className={styles.folderRow}>
          <button type="button" className={styles.folderName} disabled={!entry.readable}
            onClick={() => {if (entry.directory) void read(entry.path); else {setTyped(entry.path);onChoose(entry.path);onOpenChange(false);}}}>
            <span>{entry.name}{entry.directory ? '/' : ''}</span>
          </button>
        </li>)}
      </ul>}
      {!busy && !failed && !entries.length && <p className={styles.explorerNote}>{t('next.remoteSources.programListEmpty')}</p>}
      {path && <p className={styles.explorerNote}>{path}</p>}
    </div>
  </Dialog>;
}
