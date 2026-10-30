import {Fragment, useCallback, useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {browseServerFilesystem} from '@core/library-management.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {FilesystemPage} from '@core/administration.ts';
import {ServerDirectoryExplorer} from './ServerDirectoryExplorer';
import {Dialog} from '../overlays/Dialog';
import {Button, TextField} from '../controls';
import {Icon} from '../Icon';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type ServerFolderPickerProps = Readonly<{open: boolean; onClose: () => void; api: LibraryContentApi; initialPath?: string; onChoose: (paths: readonly string[]) => void; multiple?: boolean; purpose?: 'folder' | 'backup'; returnFocusRef?: React.RefObject<HTMLElement | null>}>;
/** The same bounded explorer serves folders and the drawn single backup file/folder picker. */
export function ServerFolderPicker(props: ServerFolderPickerProps) {
 const {onChoose, onClose} = props;
 const single = useCallback((path: string) => onChoose([path]), [onChoose]);
 const close = useCallback((open: boolean) => {if (!open) onClose();}, [onClose]);
 if (props.purpose !== 'backup') return <ServerDirectoryExplorer open={props.open} onOpenChange={close} api={props.api} initialPath={props.initialPath} returnFocusRef={props.returnFocusRef} onChoose={single} onChooseMany={props.multiple ? props.onChoose : undefined} checkWritable={!props.multiple}/>;
 return <BackupFolderPicker {...props}/>;
}

function BackupFolderPicker({open, onClose, api, initialPath = '', onChoose, multiple = false, purpose = 'folder', returnFocusRef}: ServerFolderPickerProps) {
  const backup = purpose === 'backup', many = multiple && !backup;
  const [page, setPage] = useState<FilesystemPage>(), [path, setPath] = useState(initialPath), [hidden, setHidden] = useState(false), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false), [selected, setSelected] = useState<readonly string[]>([]), [file, setFile] = useState<string>();
  const request = useRef<AbortController | undefined>(undefined), initial = useRef(initialPath); initial.current = initialPath;
  const load = useCallback(async (next: string, showHidden: boolean, cursor?: string) => {
    request.current?.abort(); const controller = new AbortController(); request.current = controller;
    setBusy(true); setFailed(false); if (!cursor) setFile(undefined);
    try {
      const result = await browseServerFilesystem(api, {path: next || undefined, showHidden, ...(backup ? {includeFiles: true} : {}), ...(cursor ? {cursor} : {})}, controller.signal);
      if (controller.signal.aborted) return;
      setPage(old => cursor && old?.path === result.path ? {...result, entries: [...old.entries, ...result.entries.filter(entry => !old.entries.some(before => before.path === entry.path))]} : result);
      setPath(result.path);
    } catch { if (!controller.signal.aborted) setFailed(true); }
    finally {if (!controller.signal.aborted) setBusy(false);}
  }, [api, backup]);
  useEffect(() => {
    if (!open) return;
    setPage(undefined); setSelected([]); setFile(undefined); setHidden(false); void load(initial.current, false);
    return () => request.current?.abort();
  }, [open, load]);
  const close = () => {request.current?.abort();setPage(undefined);setFile(undefined);setSelected([]);onClose();};
  const choose = () => {
    if (!open || !page?.path || busy || failed) return;
    if (file && !page.entries.some(entry => entry.path === file && entry.kind === 'file' && entry.readable && !entry.symlink && /\.(?:db|pba)$/i.test(entry.name))) return;
    onChoose(many && selected.length ? selected : [file ?? page.path]);close();
  };
  const segments = page?.path.split(page.separator).filter(Boolean) ?? [];
  const entries = page?.entries.filter(entry => entry.kind === 'directory' || backup && entry.kind === 'file' && /\.(?:db|pba)$/i.test(entry.name)) ?? [];
  return <Dialog open={open} onOpenChange={value => {if (!value) close();}} title={t(backup ? 'web.maintenance.chooseBackupTitle' : many ? 'next.settings.chooseFolders' : 'next.settings.chooseFolder')} description={backup ? t('web.maintenance.chooseBackupLede') : undefined} size="lg" width={backup ? 636 : 720} returnFocusRef={returnFocusRef} bodyClassName={backup ? styles.backupPickerBody : styles.folderBody} className={backup ? styles.backupPicker : undefined} headerClassName={backup ? styles.backupPickerHeader : undefined} footerClassName={backup ? styles.backupPickerFooter : undefined} showCloseButton={!backup}
    footer={<><Button size="compact" onClick={close}>{t('next.settings.cancel')}</Button><Button size="compact" variant="primary" disabled={!page?.path || busy || failed} onClick={choose}>{t(backup ? 'web.maintenance.useBackupSource' : many && selected.length ? 'next.settings.chooseSelected' : 'next.settings.chooseThisFolder', {count: selected.length})}</Button></>}>
    {!backup && <form className={styles.folderPath} onSubmit={event => {event.preventDefault(); void load(path, hidden);}}><TextField aria-label={t('next.settings.folderPath')} value={path} onChange={event => setPath(event.currentTarget.value)} /><Button type="submit" size="xs" disabled={busy}>{t('next.settings.go')}</Button></form>}
    <nav className={styles.crumbs} aria-label={t('next.settings.folderBreadcrumbs')}>{backup && <Button className={styles.backupPickerUp} size="xs" variant="icon" iconOnly icon="i-left" aria-label={t('next.settings.up')} disabled={!page?.parent || busy} onClick={() => {void load(page!.parent, hidden);}} />}<div className={backup ? styles.backupPickerCrumbs : styles.folderCrumbs}><Button size="xs" variant="text" disabled={busy} onClick={() => {void load('', hidden);}}>{backup ? page?.separator ?? '/' : t('next.settings.roots')}</Button>{segments.map((segment, index) => {
      const full = (page!.path.startsWith(page!.separator) ? page!.separator : '') + segments.slice(0, index + 1).join(page!.separator) + (page!.separator === '\\' && index === 0 ? '\\' : '');
      return <Fragment key={full}>{backup && <Icon name="i-right" size={14} />}<Button size="xs" variant="text" disabled={busy} onClick={() => {void load(full, hidden);}}>{segment}</Button></Fragment>;
    })}</div></nav>
    {!backup && <div className={styles.folderTools}><Button size="xs" disabled={!page?.parent || busy} icon="i-arrow-up" onClick={() => {void load(page!.parent, hidden);}}>{t('next.settings.up')}</Button><label><input type="checkbox" checked={hidden} onChange={event => {setHidden(event.currentTarget.checked); void load(page?.path ?? path, event.currentTarget.checked);}} />{t('next.settings.hiddenFolders')}</label></div>}
    {failed ? <div className={styles.folderNotice} role="alert">{t('next.settings.folderFailed')}<Button size="xs" onClick={() => {void load(page?.path ?? path, hidden);}}>{t('next.states.tryAgain')}</Button></div> : <div className={styles.folderEntries} aria-busy={busy}>
      {!page?.path && page?.roots.map(root => <button type="button" key={root.path} disabled={busy} onClick={() => {void load(root.path, hidden);}}><Icon name="i-folder" size={22} /><span>{root.name}<small>{root.path}</small></span><Icon name="i-right" size={18} /></button>)}
      {entries.map(entry => <div className={styles.folderEntry} key={entry.path}>
        {many && <input type="checkbox" disabled={!entry.readable || busy} checked={selected.includes(entry.path)} aria-label={t('next.settings.selectRow', {name: entry.name})} onChange={event => {const checked = event.currentTarget.checked; setSelected(old => checked ? [...old, entry.path] : old.filter(path => path !== entry.path));}} />}
        <button type="button" disabled={!entry.readable || busy || backup && entry.symlink} aria-pressed={entry.kind === 'file' ? file === entry.path : undefined} onClick={() => {if (entry.kind === 'file') setFile(entry.path);else void load(entry.path, hidden);}}><Icon name={entry.kind === 'file' ? 'i-storage' : 'i-folder'} size={backup ? 20 : 22} /><span>{entry.name}{!entry.readable && !backup && <small>{t('next.settings.folderUnreadable')}</small>}</span>{entry.kind === 'file' ? <><small className={styles.backupFileSize}>{defaultI18n.bytes(entry.bytes)}</small>{backup && <i aria-hidden="true" />}</> : <>{backup && <i aria-hidden="true" />}<Icon name="i-right" size={18} /></>}</button>
      </div>)}
      {page?.path && entries.length === 0 && !busy && <p className={styles.folderNotice}>{t(backup ? 'web.maintenance.noBackupSources' : 'next.settings.noFolders')}</p>}
      {page?.truncated && <p className={styles.folderNotice}>{t('next.settings.foldersTruncated')}</p>}
      {page?.nextCursor && <Button size="xs" disabled={busy} onClick={() => {void load(page.path, hidden, page.nextCursor);}}>{t('next.settings.loadMore')}</Button>}
    </div>}
    {busy && <p className={styles.folderNotice} role="status">{t('next.settings.loading')}</p>}
  </Dialog>;
}
