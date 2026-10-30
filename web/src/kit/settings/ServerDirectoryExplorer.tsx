import {useCallback, useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {browseServerFilesystem, classifyServerPath} from '@core/library-management.ts';
import {Button, Switch, TextField} from '../controls';
import {Icon, type IconName} from '../Icon';
import {BottomSheet} from '../overlays/BottomSheet';
import {Dialog} from '../overlays/Dialog';
import {usePhone} from '../overlays/shared';
import {Skeleton} from '../states';
import styles from './server-directory-explorer.module.css';

const t = defaultI18n.t;

/** One open Places sheet: the explorer attempt and the exact response that
 * published its roots. A later attempt or response never adopts it. */
type FilesystemPage = NonNullable<Awaited<ReturnType<typeof browseServerFilesystem>>>;
type PlacesOpening = Readonly<{attempt: object; page: FilesystemPage}>;

/** A root's glyph follows the server's own word for it: a drive is a disk, a
 * share or network mount is the server, volumes are storage, Home is the house. */
function rootIcon(root: Readonly<{kind: string; description: string}>): IconName {
  if (root.kind === 'home') return 'i-house';
  if (root.kind === 'unc' || root.kind === 'volumes') return 'i-server';
  if (/^(network drive|mounted (nfs|cifs|smb|afp|sshfs|webdav))|\\\\/i.test(root.description)) return 'i-server';
  if (root.kind === 'mount' || root.kind === 'volume' || root.kind === 'configured') return 'i-storage';
  return 'i-disk';
}

/** The server's own folder explorer, folders only (owner-libraries.html#9). A
 * typed path is always available; Places are the roots the server publishes. */
export function ServerDirectoryExplorer({open, onOpenChange, api, onChoose, onChooseMany, initialPaths, initialPath, checkWritable = false, returnFocusRef, serverName}: Readonly<{
  open: boolean; onOpenChange: (open: boolean) => void; api: LibraryContentApi; onChoose: (path: string, classification?: 'local' | 'network') => void; onChooseMany?: (paths: readonly string[], classifications?: Readonly<Record<string, 'local' | 'network'>>) => void; initialPaths?: readonly string[]; initialPath?: string; checkWritable?: boolean; returnFocusRef?: import('react').RefObject<HTMLElement | null>;
  /** The real server this explorer is browsing, for the drawn subtitle. When the
   * caller has none, the dialog keeps its truthful generic label. */
  serverName?: string;
}>) {
  const phone = usePhone();
  const [chosen, setChosen] = useState<readonly string[]>([]), [typing, setTyping] = useState(false), [placesOpening, setPlacesOpening] = useState<PlacesOpening>();
  const [attempt, setAttempt] = useState<object>();
  const activeAttempt = useRef<object | undefined>(undefined);
  useLayoutEffect(() => {
    if (!open) return;
    const next = {}; activeAttempt.current = next; setAttempt(next);
    setChosen([...new Set(initialPaths ?? [])].slice(0, 64)); setTyping(false); setPage(undefined); setUncPath('');
    return () => {if (activeAttempt.current === next) activeAttempt.current = undefined; request.current?.abort();};
  }, [open, api, onChoose, onChooseMany, initialPaths, initialPath]);
  const current = () => open && !!attempt && activeAttempt.current === attempt;
  const placesRef = useRef<PlacesOpening | undefined>(undefined);
  useLayoutEffect(() => {
    placesRef.current = placesOpening;
    return () => {if (placesRef.current === placesOpening) placesRef.current = undefined;};
  }, [placesOpening]);
  useLayoutEffect(() => {if (!open || !phone) {placesRef.current = undefined;setPlacesOpening(undefined);}}, [open, phone]);
  useLayoutEffect(() => {placesRef.current = undefined;setPlacesOpening(undefined);}, [attempt]);
  const samePlaces = (opening: PlacesOpening) => placesRef.current === opening && current() && phone && opening.attempt === attempt;
  const closePlaces = (opening: PlacesOpening) => {if (placesRef.current !== opening) return;placesRef.current = undefined;setPlacesOpening(undefined);};
  const openPlaces = () => {
    if (!current() || !phone || !attempt || !page || !page.roots.length) return;
    setPlacesOpening(Object.freeze({attempt, page}));
  };
  const pickPlace = (opening: PlacesOpening, path: string) => {
    if (!samePlaces(opening) || page !== opening.page || !opening.page.roots.some(root => root.path === path)) return;
    closePlaces(opening);
    const root = opening.page.roots.find(value => value.path === path);
    if (!root) return;
    if (root.kind === 'unc') {setTyping(true); setTyped(root.path);} else go(root.path);
  };
  const [page, setPage] = useState<Awaited<ReturnType<typeof browseServerFilesystem>>>();
  useLayoutEffect(() => {if (placesOpening && placesOpening.page !== page) {placesRef.current = undefined;setPlacesOpening(undefined);}}, [page, placesOpening]);
  const [typed, setTyped] = useState(''), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false);
  const [uncPath, setUncPath] = useState('');
  const [showHidden, setShowHidden] = useState(false);
  const suggestionId = useId();
  const [suggestions, setSuggestions] = useState<readonly string[]>([]);
  useEffect(() => {
    setSuggestions([]);
    if (!open || !typed || typed === page?.path || !page?.separator) return;
    const boundary = typed.lastIndexOf(page.separator);
    if (boundary < 0) return;
    const parent = typed.slice(0, boundary + page.separator.length);
    const prefix = typed.slice(boundary + page.separator.length);
    if (prefix.length > 255) return;
    const active = new AbortController();
    const timer = setTimeout(() => {
      void browseServerFilesystem(api, {path: parent, prefix, includeFiles: false, showHidden, limit: 8}, active.signal).then(next => {
        if (!active.signal.aborted) setSuggestions(next.entries.filter(entry => entry.kind === 'directory' && entry.readable).map(entry => entry.path));
      }, () => {});
    }, 250);
    return () => { clearTimeout(timer); active.abort(); };
  }, [api, open, typed, page?.path, page?.separator, showHidden]);
  const request = useRef<AbortController | undefined>(undefined);
  const read = useCallback(async (path?: string, hidden = false, cursor?: string) => {
    request.current?.abort();
    const active = new AbortController();
    request.current = active;
    // A pending or failed destination must never leave the previous folder choosable.
    if (!cursor) { setPage(undefined); setTyped(path ?? ''); }
    setBusy(true); setFailed(false);
    try {
      const next = await browseServerFilesystem(api, {...(path ? {path} : {}), includeFiles: false, checkWritable, showHidden: hidden, limit: 200, ...(cursor ? {cursor} : {})}, active.signal);
      if (active.signal.aborted || request.current !== active) return;
      setPage(previous => cursor && previous?.path === next.path
        ? {...next, entries: [...previous.entries, ...next.entries.filter(entry => !previous.entries.some(value => value.path === entry.path))]}
        : next);
      setTyped(next.path);
    } catch {
      if (!active.signal.aborted && request.current === active) setFailed(true);
    } finally {
      if (!active.signal.aborted && request.current === active) setBusy(false);
    }
  }, [api, checkWritable]);
  useEffect(() => {
    if (open && attempt && activeAttempt.current === attempt) { setShowHidden(false); void read(initialPath); }
    return () => { request.current?.abort(); request.current = undefined; };
  }, [open, read, attempt, initialPath]);
  const changeOpen = (next: boolean) => {
    if (!current()) return;
    if (!next) { activeAttempt.current = undefined; request.current?.abort(); request.current = undefined; }
    onOpenChange(next);
  };
  const go = (path: string) => { if (current()) {setTyping(false); void read(path, showHidden);} };
  const toggle = (path: string) => {if (!current()) return; setChosen(previous => previous.includes(path) ? previous.filter(value => value !== path) : previous.length < 64 ? [...previous, path] : previous);};
  const canChooseCurrent = current() && !!page?.path && typed === page.path && !busy && !failed;
  const submit = () => {
    if (!current()) return;
    if (onChooseMany) {
      if (!chosen.length) return;
      const classifications: Record<string, 'local' | 'network'> = {};
      for (const path of chosen) {
        const classification = page ? classifyServerPath(path, page.roots, page.separator) : undefined;
        if (classification) classifications[path] = classification;
      }
      onChooseMany(Object.freeze([...chosen]), Object.freeze(classifications));
      changeOpen(false);
    } else if (canChooseCurrent && page) {
      onChoose(page.path, classifyServerPath(page.path, page.roots, page.separator));
      changeOpen(false);
    }
  };

  const breadcrumbs: {path: string; label: string}[] = [];
  if (page?.path) {
    const separator = page.separator;
    const root = [...page.roots].sort((a, b) => b.path.length - a.path.length).find(value =>
      page.path === value.path || page.path.startsWith(value.path.endsWith(separator) ? value.path : value.path + separator));
    if (root) {
      breadcrumbs.push({path: root.path, label: root.name});
      let path = root.path;
      for (const part of page.path.slice(root.path.length).split(separator).filter(Boolean)) {
        path = path.endsWith(separator) ? path + part : path + separator + part;
        breadcrumbs.push({path, label: part});
      }
    }
  }
  const places = page?.roots ?? [];
  const placeButton = (root: (typeof places)[number], onPicked?: () => void) => <button type="button" className={styles.pickerPlace} disabled={busy} aria-current={page?.path === root.path ? 'location' : undefined} onClick={() => {
    if (!current()) return;
    if (onPicked) {onPicked();return;}
    if (root.kind === 'unc') {setTyping(true); setTyped(root.path);} else go(root.path);
  }}><span className={styles.pickerPlaceIcon}><Icon name={rootIcon(root)} size={20}/></span><span className={styles.pickerPlaceCopy}><b>{root.name}</b><small>{root.description || root.path}</small></span></button>;
  const rootCard = (root: (typeof places)[number]) => root.kind === 'unc'
    ? <div key={root.path} className={styles.pickerRootWide}>
      <span className={styles.pickerPlaceIcon}><Icon name={rootIcon(root)} size={24}/></span>
      <div className={styles.pickerRootBody}>
        <b>{root.name}</b><small>{root.description || root.path}</small>
        <div className={styles.pickerUnc}>
          <TextField label={t('next.ownerLibraries.explorer.uncPath')} value={uncPath} disabled={busy} autoComplete="off" spellCheck={false} placeholder={root.path}
            onChange={event => {if (current()) setUncPath(event.currentTarget.value);}} onKeyDown={event => {
              if (event.key === 'Enter' && !event.nativeEvent.isComposing && !busy && uncPath.trim()) {event.preventDefault(); go(uncPath.trim());}
            }}/>
          <Button disabled={busy || !uncPath.trim()} onClick={() => {if (current()) go(uncPath.trim());}}>{t('next.ownerLibraries.explorer.open')}</Button>
        </div>
        <p className={styles.pickerNote}>{t('next.ownerLibraries.explorer.uncHelp')}</p>
      </div>
    </div>
    : <button key={root.path} type="button" className={styles.pickerPlace} disabled={busy} onClick={() => {if (current()) go(root.path);}}>
      <span className={styles.pickerPlaceIcon}><Icon name={rootIcon(root)} size={24}/></span>
      <span className={styles.pickerPlaceCopy}><b>{root.name}</b><small>{root.description || root.path}</small></span>
    </button>;
  const title = t(onChooseMany ? 'next.ownerLibraries.explorer.title' : 'next.settings.chooseFolder'), subtitle = serverName?.trim() ? t('next.ownerLibraries.explorer.subtitle', {server: serverName.trim()}) : t('next.settings.folderBreadcrumbs') as string;
  return <><Dialog open={open} onOpenChange={changeOpen} size="lg" width={1000} responsive={false} showCloseButton={!phone} returnFocusRef={returnFocusRef} className={styles.explorer} bodyClassName={styles.explorerBody} footerClassName={styles.explorerFooter} title={title} description={subtitle}
    footer={<>
      {onChooseMany ? <ChosenServerFolders paths={chosen} onRemove={toggle}/> : null}
      <Button onClick={() => changeOpen(false)}>{t('next.settings.cancel')}</Button>
      <Button variant="primary" disabled={onChooseMany ? !current() || !chosen.length : !canChooseCurrent} onClick={submit}>{onChooseMany && chosen.length ? t('next.ownerLibraries.explorer.useFolders', {count: chosen.length}) : t('next.ownerLibraries.explorer.useFolder')}</Button>
    </>}>
    <div className={styles.picker}>
      {phone ? <><div className={styles.phoneTop}>
        <button type="button" className={styles.phoneAction} onClick={() => changeOpen(false)}>{t('next.shell.cancel')}</button>
        <b>{title}</b>
        <button type="button" className={styles.phoneDone} disabled={onChooseMany ? !chosen.length : !canChooseCurrent} onClick={submit}>{t('next.settings.done')}</button>
      </div>
      <p className={styles.phoneSub}>{subtitle}</p></> : null}
      <div className={styles.pickerPath}>
        {phone ? <Button icon="i-disk" onClick={openPlaces}>{t('next.ownerLibraries.explorer.places')}</Button>
          : <Button disabled={busy || !page?.parent} aria-label={t('next.ownerLibraries.explorer.up')} onClick={() => {if (page?.parent) go(page.parent);}}>{t('next.settings.up')}</Button>}
        {typing || !breadcrumbs.length ? <TextField label={t('next.settings.folderPath')} list={suggestionId} autoComplete="off" disabled={busy} error={failed ? t('next.ownerLibraries.explorer.failed') : undefined} value={typed} onChange={event => {if (current()) {setTyped(event.currentTarget.value);setFailed(false);}}} onKeyDown={event => {
          if (event.key === 'Enter' && !event.nativeEvent.isComposing && !busy) {event.preventDefault(); go(typed);}
        }}/> : <nav className={styles.pickerBreadcrumbs} aria-label={t('next.settings.folderPath')}>
          {breadcrumbs.map((crumb, index) => <Button key={crumb.path} variant="text" size="small" disabled={busy} aria-current={index === breadcrumbs.length - 1 ? 'location' : undefined} onClick={() => go(crumb.path)}>{crumb.label}</Button>)}
          <Button variant="text" size="small" onClick={() => {if (current()) setTyping(true);}}>{t('next.ownerLibraries.explorer.typePath')}</Button>
        </nav>}
        {typing || !breadcrumbs.length ? <Button disabled={busy || !typed} onClick={() => go(typed)}>{t('next.settings.go')}</Button> : null}
        {onChooseMany && !phone ? <Button disabled={!canChooseCurrent || !!page && !chosen.includes(page.path) && chosen.length >= 64} onClick={() => {if (canChooseCurrent && page && !chosen.includes(page.path)) toggle(page.path);}}>{t('next.settings.chooseThisFolder')}</Button> : null}
        <datalist id={suggestionId}>{suggestions.map(path => <option key={path} value={path}/>)}</datalist>
      </div>
      {phone && onChooseMany && page?.path && canChooseCurrent ? <button type="button" className={styles.addThis} aria-pressed={chosen.includes(page.path)} disabled={!chosen.includes(page.path) && chosen.length >= 64} onClick={() => toggle(page.path)}>
        <span className={styles.addCheck} data-on={chosen.includes(page.path) || undefined}>{chosen.includes(page.path) ? <Icon name="i-check" size={13}/> : null}</span>
        <Icon name="i-folder" size={20}/>
        <span className={styles.addCopy}><b>{t('next.ownerLibraries.explorer.addThisFolder')}</b><small>{page.path}</small></span>
      </button> : null}
      <div className={styles.explorerColumns} data-roots={!page?.path || undefined}>
        {!phone && !!page?.path && places.length > 0 ? <nav className={styles.pickerPlaces} aria-label={t('next.ownerLibraries.explorer.places')}>
          <h4>{t('next.ownerLibraries.explorer.places')}</h4>{places.map(root => <div key={root.path}>{placeButton(root)}</div>)}
        </nav> : null}
        <section className={styles.explorerMain} aria-label={t('next.ownerLibraries.explorer.folders')}>
          {page ? <header className={styles.pickerListHeader}>{page.path ? <><b>{t('next.ownerLibraries.explorer.folders')}</b><label>{t('next.ownerLibraries.explorer.showHidden')}<Switch aria-label={t('next.ownerLibraries.explorer.showHidden')} checked={showHidden} disabled={busy} onCheckedChange={value => {if (current()) {setShowHidden(value); void read(page.path, value);}}}/></label></> : <b>{t('next.ownerLibraries.explorer.whereToLook')}</b>}</header> : null}
          {failed ? <div role="alert" className={styles.pickerNote}><p>{t('next.ownerLibraries.explorer.failed')}</p><p>{t('next.ownerLibraries.explorer.failedHelp')}</p></div> : null}
          {busy && !page ? <div aria-busy="true"><p role="status">{t('next.ownerLibraries.explorer.reading')}</p>{[0, 1, 2].map(index => <Skeleton key={index} height={52}/>)}</div> : null}
          {page && !page.path ? <div className={styles.pickerRoots}>{places.map(root => rootCard(root))}</div> : null}
          {page?.path ? <ul className={styles.pickerList}>{page.entries.filter(entry => entry.kind === 'directory').map(entry => <li key={entry.path} className={styles.pickerEntry} data-hidden={entry.hidden || undefined}>
            {onChooseMany ? <input type="checkbox" aria-label={entry.path} checked={chosen.includes(entry.path)} disabled={!entry.readable || busy || !chosen.includes(entry.path) && chosen.length >= 64} onChange={() => toggle(entry.path)}/> : null}
            <button type="button" className={styles.pickerRow} disabled={!entry.readable || busy} onClick={() => go(entry.path)}><span>{entry.name}</span></button>
            {entry.hidden ? <small>{t('next.ownerLibraries.explorer.hidden')}</small> : null}{entry.symlink ? <small>{t('next.ownerLibraries.explorer.link')}</small> : null}{!entry.readable ? <small>{t('next.ownerLibraries.explorer.noAccess')}</small> : null}
          </li>)}</ul> : null}
          {page?.path && !page.entries.length ? <p className={styles.pickerNote}>{t('next.ownerLibraries.explorer.noFolders')}</p> : null}
          {page?.truncated && !page.nextCursor ? <p className={styles.pickerNote}>{t('next.ownerLibraries.explorer.truncated')}</p> : null}
          {checkWritable && page?.path && page.writableChecked ? <p className={styles.pickerNote}>{t(page.writable ? 'next.ownerLibraries.explorer.writable' : 'next.ownerLibraries.explorer.notWritable')}</p> : null}
          {page?.nextCursor ? <Button disabled={busy} onClick={() => {if (current()) void read(page.path, showHidden, page.nextCursor);}}>{t('next.ownerLibraries.explorer.more')}</Button> : null}
        </section>
      </div>
      {phone ? <div className={styles.phoneTray}>
        {onChooseMany ? <ChosenServerFolders paths={chosen} onRemove={toggle} disabled={busy}/> : null}
        <Button variant="primary" disabled={onChooseMany ? !current() || !chosen.length : !canChooseCurrent} onClick={submit}>{onChooseMany && chosen.length ? t('next.ownerLibraries.explorer.useFolders', {count: chosen.length}) : t('next.ownerLibraries.explorer.useFolder')}</Button>
      </div> : null}
    </div>
  </Dialog>
  {phone && placesOpening && current() && placesOpening.attempt === attempt && placesOpening.page === page ? <BottomSheet open onOpenChange={next => {if (!next) closePlaces(placesOpening);}} title={t('next.ownerLibraries.explorer.places')} fitContent>
    <div className={styles.phonePlaces}>{placesOpening.page.roots.map(root => <div key={root.path}>{placeButton(root, () => pickPlace(placesOpening, root.path))}</div>)}</div>
  </BottomSheet> : null}</>;
}

/** The same captured paths remain removable in the parent library form. */
export function ChosenServerFolders({paths, onRemove, disabled = false}: Readonly<{paths: readonly string[]; onRemove: (path: string) => void; disabled?: boolean}>) {
 return <div className={styles.chosenFolders} aria-label={t('next.ownerLibraries.explorer.chosen')}>
  <span>{t('next.ownerLibraries.explorer.chosen')}</span>{!paths.length ? <span>{t('next.ownerLibraries.explorer.noneChosen')}</span> : null}
  {paths.map(path => <Button key={path} size="small" variant="glass" icon="i-x" disabled={disabled} aria-label={`${t('next.ownerLibraries.explorer.removeChosen')} ${path}`} onClick={() => onRemove(path)}>{path}</Button>)}
 </div>;
}
