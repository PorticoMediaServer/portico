import {useCallback, useEffect, useId, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {RemoteFolder, RemoteFolderPage, RemoteSourceService} from '@core/remote-sources.ts';
import {Button, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {usePhone} from '../../../kit/overlays/shared';
import {Icon} from '../../../kit/Icon';
import styles from './remote-sources.module.css';

const t = defaultI18n.t;
const PAGE = 500;
const cleanPath = (value: string) => value.trim().replace(/^\/+|\/+$/g, '');

/** The folder explorer inside a saved source (remote-sources.html#18, #19, #20):
 * the same dialog as the server's disks, pointed at the storage. It lists one
 * folder at a time as the owner opens it, selects several, and downloads nothing. */
export function RemoteFolderPicker({service, sourceId, sourceName, open, onOpenChange, onChoose}: Readonly<{
  service: RemoteSourceService; sourceId: string; sourceName: string; open: boolean;
  onOpenChange: (open: boolean) => void; onChoose: (folders: readonly RemoteFolder[]) => void;
}>) {
  const phone = usePhone();
  const [page, setPage] = useState<RemoteFolderPage>();
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState<'' | 'list' | 'more'>('');
  const [showHidden, setShowHidden] = useState(false);
  const [chosen, setChosen] = useState<readonly RemoteFolder[]>([]);
  const request = useRef<AbortController | undefined>(undefined);
  // Completion offers only the folders this page already listed: no extra read,
  // no guess at a path the storage has not shown.
  const pathsId = useId();
  const read = useCallback(async (relative: string | undefined, hidden: boolean, cursor?: string) => {
    request.current?.abort();
    const active = new AbortController();
    request.current = active;
    // A pending or failed destination must never leave the previous folder choosable.
    if (!cursor) { setPage(undefined); setTyped(relative ?? ''); }
    setBusy(true); setFailed('');
    try {
      const next = await service.listFolders(sourceId, {...(relative ? {path: relative} : {}), showHidden: hidden, limit: PAGE, ...(cursor ? {cursor} : {})});
      if (active.signal.aborted || request.current !== active) return;
      setPage(previous => cursor && previous && previous.relative === next.relative
        ? {...next, entries: [...previous.entries, ...next.entries.filter(entry => !previous.entries.some(value => value.relative === entry.relative))]}
        : next);
      if (!cursor) setTyped(next.relative);
    } catch {
      if (!active.signal.aborted && request.current === active) setFailed(cursor ? 'more' : 'list');
    } finally {
      if (!active.signal.aborted && request.current === active) setBusy(false);
    }
  }, [service, sourceId]);
  useEffect(() => {
    if (open) { setChosen([]); setShowHidden(false); setFailed(''); void read(undefined, false); }
    return () => { request.current?.abort(); request.current = undefined; };
  }, [open, read]);
  const close = () => { request.current?.abort(); request.current = undefined; onOpenChange(false); };
  const toggle = (folder: RemoteFolder) => setChosen(current => current.some(value => value.relative === folder.relative)
    ? current.filter(value => value.relative !== folder.relative)
    : [...current, folder]);
  const crumbs = page?.relative ? page.relative.split('/') : [];
  const parent = crumbs.length > 1 ? crumbs.slice(0, -1).join('/') : undefined;
  const folderName = page && page.relative ? crumbs[crumbs.length - 1] : sourceName;
  const chooseCurrent = () => {
    if (!page || busy || failed) return;
    const current: RemoteFolder = {name: folderName, path: page.path, relative: page.relative, hidden: folderName.startsWith('.')};
    if (!chosen.some(value => value.relative === current.relative)) setChosen([...chosen, current]);
  };
  const choose = () => { if (chosen.length) { onChoose(chosen); close(); } };
  const listed = page?.entries;
  const label = (folder: RemoteFolder) => `${sourceName}: /${folder.relative}`;
  // Several folders are chosen here, so the phone page keeps the chosen list in its body:
  // the dialog's footer is replaced by the drawn Cancel/title/Done bar on a full window.
  const chosenList = <div className={styles.chosenList}>
    {chosen.length > 0 && <span className={styles.chosenLabel}>{t('next.remoteSources.chosen')}</span>}
    {chosen.map(folder => <button key={folder.relative} type="button" className={styles.chosenChip} onClick={() => toggle(folder)}
      aria-label={t('next.remoteSources.removeFolder', {name: label(folder)})}>
      <Icon name="i-folder" size={15}/>{label(folder)}<Icon name="i-x" size={14}/>
    </button>)}
  </div>;
  return <Dialog open={open} onOpenChange={next => {if (!next) close();}} width={760} title={t('next.remoteSources.foldersTitle')}
    responsive={false} showCloseButton={!phone}
    className={phone ? styles.folderPickerPhone : undefined} bodyClassName={phone ? styles.folderPickerBodyPhone : undefined}
    description={phone ? undefined : t('next.remoteSources.foldersIn', {name: sourceName})}
    footer={phone ? undefined : <>
      {chosenList}
      <Button size="small" variant="glass" onClick={close}>{t('next.remoteSources.cancel')}</Button>
      <Button size="small" variant="primary" disabled={!chosen.length} onClick={choose}>
        {t(chosen.length === 1 ? 'next.remoteSources.useOneFolder' : 'next.remoteSources.useFolders', {count: chosen.length})}
      </Button>
    </>}>
    {phone && <div className={styles.folderPhoneTop}>
      <button type="button" className={styles.folderPhoneAction} onClick={close}>{t('next.remoteSources.cancel')}</button>
      <b>{t('next.remoteSources.foldersTitle')}</b>
      <button type="button" className={styles.folderPhoneDone} disabled={!chosen.length} onClick={choose}>{t('next.settings.done')}</button>
    </div>}
    {phone && <div className={styles.folderPhoneChosen}>{chosenList}</div>}
    <div className={styles.explorer}>
      <div className={styles.explorerPath}>
        <Button size="small" variant="glass" disabled={busy || !page} aria-label={t('next.remoteSources.upOneFolder')}
          onClick={() => void read(parent, showHidden)}><Icon name="i-up" size={18}/></Button>
        <nav aria-label={t('next.remoteSources.folderPath')}>
          <Button size="small" variant="text" disabled={busy} aria-current={crumbs.length === 0 ? 'location' : undefined}
            onClick={() => void read(undefined, showHidden)}>{sourceName}</Button>
          {crumbs.map((crumb, index) => {
            const target = crumbs.slice(0, index + 1).join('/');
            return <span key={target}><Icon name="i-right" size={14}/><Button size="small" variant="text" disabled={busy}
              aria-current={index === crumbs.length - 1 ? 'location' : undefined} onClick={() => void read(target, showHidden)}>{crumb}</Button></span>;
          })}
        </nav>
        <Button size="small" variant="glass" disabled={busy || !page || !!failed} onClick={chooseCurrent}>{t('next.remoteSources.chooseThisFolder')}</Button>
      </div>
      <div className={styles.explorerHeader}>
        <span>{t('next.remoteSources.foldersHeader', {name: folderName})}</span>
        <label className={styles.hiddenToggle}><span>{t('next.remoteSources.showHiddenFolders')}</span>
          <Switch aria-label={t('next.remoteSources.showHiddenFolders')} checked={showHidden} disabled={busy}
            onCheckedChange={value => {setShowHidden(value); void read(page?.relative || undefined, value);}}/></label>
      </div>
      {!failed && <p className={styles.explorerNote}><Icon name="i-info" size={18}/>{t('next.remoteSources.foldersNote')}</p>}
      <TextField label={t('next.remoteSources.folderPath')} value={typed} disabled={busy} autoCapitalize="off" spellCheck={false}
        list={pathsId} autoComplete="off"
        className={styles.monoInput} onChange={event => setTyped(event.currentTarget.value)}
        onKeyDown={event => {
          if (event.key === 'Enter' && !event.nativeEvent.isComposing && !busy) {
            event.preventDefault();
            void read(cleanPath(typed) || undefined, showHidden);
          }
        }}/>
      <datalist id={pathsId}>{page?.entries.map(entry => <option key={entry.relative} value={entry.relative}/>)}</datalist>
      {failed && <div className={styles.explorerError} role="alert"><Icon name="i-warn" size={20}/>
        <div><b>{t('next.remoteSources.folderListFailedTitle', {name: folderName})}</b>
          <p>{t('next.remoteSources.folderListFailedHelp')}</p>
          <Button size="xs" variant="glass" disabled={busy} onClick={() => void read(failed === 'more' ? page?.relative || undefined : cleanPath(typed) || undefined, showHidden, failed === 'more' ? page?.nextCursor : undefined)}>{t('next.remoteSources.retry')}</Button>
        </div></div>}
      {busy && !page && <>
        <div className={styles.skeleton} aria-hidden="true">
          {[0, 1, 2, 3, 4].map(row => <div key={row} className={styles.skeletonRow}>
            <span className={styles.skeletonBox}/><span className={styles.skeletonBox}/><span className={styles.skeletonShine} data-width={row % 2 ? 'short' : 'long'}/>
          </div>)}
        </div>
        <p className={styles.explorerNote} role="status">{t('next.remoteSources.folderListing')}</p>
      </>}
      {listed && listed.length > 0 && <ul className={styles.folderList}>
        {listed.map(folder => {
          const picked = chosen.some(value => value.relative === folder.relative);
          return <li key={folder.relative} className={styles.folderRow} data-picked={picked || undefined}>
            <button type="button" role="checkbox" aria-checked={picked} className={styles.folderCheck}
              aria-label={folder.name} onClick={() => toggle(folder)}>
              <span className={styles.checkBox}>{picked && <Icon name="i-check" size={14}/>}</span>
            </button>
            <button type="button" className={styles.folderName} onClick={() => void read(folder.relative, showHidden)}>
              <Icon name="i-folder" size={20}/><span>{folder.name}</span>
            </button>
            <button type="button" className={styles.folderOpen} aria-label={t('next.remoteSources.openFolder', {name: folder.name})}
              onClick={() => void read(folder.relative, showHidden)}><Icon name="i-right" size={18}/></button>
          </li>;
        })}
      </ul>}
      {page && !busy && !failed && listed && listed.length === 0 && <div className={styles.folderEmpty}>
        <Icon name="i-folder" size={26}/><b>{t('next.remoteSources.noFoldersHere')}</b><p>{t('next.remoteSources.noFoldersHereHelp')}</p>
      </div>}
      {page?.truncated && <p className={styles.explorerNote} role="status"><Icon name="i-info" size={18}/>{t('next.remoteSources.folderTruncated')}</p>}
      {page?.nextCursor && <div className={styles.moreRow}>
        <Button size="xs" variant="glass" disabled={busy} onClick={() => void read(page.relative || undefined, showHidden, page.nextCursor)}>{t('next.remoteSources.showMoreFolders')}</Button>
      </div>}
    </div>
  </Dialog>;
}
