import {dependsOn} from '@core/freshness-hub.ts';
import {useFreshness} from '../../../platform/freshness';
import {mountQuietInput, SettingsOpenContext} from '../quiet-refresh';
import {useContext, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {LibraryManagementService, type ManagedLibrary} from '@core/library-management.ts';
import {reorderLibraries} from '@core/server-admin/libraries.ts';
import {metadataAgentPath, parseMetadataAgent} from '@core/server-admin/metadata-agent.ts';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays/Popover';
import {Icon, type IconName} from '../../../kit/Icon';
import {router} from '../../../router';
import {StateView} from '../../../kit/states';
import styles from './libraries.module.css';
import {libraryRows, movedIds, movedLibraryIds, type LibraryKind} from './model';
import {AddLibrary} from './AddLibrary';
import {RemoveLibrary} from './RemoveLibrary';

const t = defaultI18n.t;
const kindIcons: Readonly<Record<LibraryKind, IconName>> = {movie: 'i-film', tv: 'i-tvlib', anime: 'i-tvlib', music: 'i-music', audiobook: 'i-headph', book: 'i-sbook', comic: 'i-page'};

/** The owner's Libraries settings list. Server facts only: each library's name,
 * its folder and whether a scan is running. A scan is started here, from the
 * row the owner chose, and the page never shows scan progress or counts.
 *
*/
export function LibrariesPanel({api, service, onEdit, onError, serverName}: {
  api: LibraryContentApi; service: LibraryManagementService; onEdit: (libraryId: string) => void; onError: (error: unknown) => void; serverName?: string;
}) {
  const {hub}=useFreshness();
  const visibility=useContext(SettingsOpenContext);
  useEffect(() => {
    const input=mountQuietInput(hub,'settings/libraries',[dependsOn.libraries,dependsOn.settings('server')],{
      id:'directory',refresh:service.refreshDirectory.bind(service),cancel:service.invalidateQuietDirectory,subscribe:service.subscribe,
      blocked:() => service.getSnapshot().directory.phase==='loading' || service.getSnapshot().mutation.phase==='pending',
      settlement:() => service.getSnapshot().mutation,
    },visibility);
    return () => input.dispose();
  },[hub,service,visibility]);
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const [busy, setBusy] = useState<string>(), [menu, setMenu] = useState<string>(), [adding, setAdding] = useState(false), [removing, setRemoving] = useState<string>();
  const directory = snapshot.directory;
  const libraries = directory.data?.items ?? [];
  const list = useRef<HTMLOListElement>(null);
  const drag = useRef<{id: string; pointer: number; to: number; libraries: readonly ManagedLibrary[]; api: LibraryContentApi; service: LibraryManagementService} | undefined>(undefined);
  const [dropTarget, setDropTarget] = useState<string>(), [announcement, setAnnouncement] = useState('');
  const menuLibrary = libraries.find(library => library.id === menu);
  const [agentFact, setAgentFact] = useState<Readonly<{api: LibraryContentApi; library: ManagedLibrary; online: boolean}>>();
  useEffect(() => {
    if (!menuLibrary) return;
    let live = true;
    void api.request<unknown>(metadataAgentPath(menuLibrary.id), 'GET').then(raw => {
      if (!live) return;
      const agent = parseMetadataAgent(raw);
      setAgentFact({api, library: menuLibrary, online: agent.libraryId === menuLibrary.id && agent.libraryKind === menuLibrary.kind && agent.agent === 'online'});
    }).catch(() => {if (live) setAgentFact({api, library: menuLibrary, online: false});});
    return () => {live = false;};
  }, [api, menuLibrary]);
  const onlineMenu = agentFact?.api === api && agentFact.library === menuLibrary && agentFact.online;
  const lastScanLabel = (id: string) => {
    const job = libraries.find(library => library.id === id)?.lastScan;
    if (!job) return t('next.ownerLibraries.neverScanned');
    if ((job.status === 'complete' || job.status === 'complete_with_warnings') && job.finishedAt && Number.isFinite(Date.parse(job.finishedAt))) return t('server.libraries.scanned', {when: defaultI18n.dateTime(job.finishedAt)});
    return undefined;
  };
  useEffect(() => { void service.loadLibraries().catch(onError); }, [service, onError]);
  const lifetime = useRef<{running: boolean} | undefined>(undefined);
  useEffect(() => {
    const capture = {running: false};
    lifetime.current = capture;
    setBusy(undefined);
    return () => {if (lifetime.current === capture) lifetime.current = undefined;};
  }, [api, service]);
  const run = async (libraryId: string, command: (current: () => boolean) => Promise<void>) => {
    const capture = lifetime.current;
    if (!capture || capture.running) return;
    capture.running = true;
    const current = () => lifetime.current === capture;
    setBusy(libraryId);
    try { await command(current); }
    catch (error) { if (current()) onError(error); }
    finally { if (current()) {capture.running = false; setBusy(undefined);} }
  };
  const scan = (library: ManagedLibrary) => run(library.id, async current => {
    await service.selectLibrary(library.id);
    if (!current()) return;
    const selected = service.getSnapshot().selected;
    if (selected.phase !== 'ready' || selected.data?.library.id !== library.id) throw new Error('library_changed');
    await service.scan();
    if (!current()) return;
    if (service.getSnapshot().mutation.phase !== 'complete') throw new Error('library_scan_failed');
    await service.loadLibraries();
  });
  const reorder = (library: ManagedLibrary, next: readonly string[] | undefined) => {
    if (!next) return;
    return run(library.id, async current => {
      await reorderLibraries(api, next);
      if (current()) {
        await service.loadLibraries();
        if (current()) setAnnouncement(t('next.settings.moved', {name: library.name, position: next.indexOf(library.id) + 1, count: next.length}));
      }
    });
  };
  const move = (library: ManagedLibrary, offset: -1 | 1) => reorder(library, movedIds(libraries, library.id, offset));
  const cancelDrag = () => {drag.current = undefined; setDropTarget(undefined);};
  if (directory.phase === 'error' || directory.phase === 'refresh-required') {
    return <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => { void service.loadLibraries().catch(onError); }}} />;
  }
  if (!directory.data) return <p role="status" className={styles.loading}>{t('next.settings.loading')}</p>;
  const rows = libraryRows(libraries);
  return <>
    <div className={styles.listHeader}>
      <Button variant="primary" size="small" icon="i-plus" disabled={!!busy} onClick={() => { if (!lifetime.current?.running) setAdding(true); }}>{t('next.settingsSpec.server.libraries.groups.0.rows.0.control.addLabel')}</Button>
    </div>
    {!libraries.length && <p className={styles.empty}>{t('next.settings.libraries.empty')}</p>}
    <ol ref={list} className={styles.list} aria-label={t('next.settingsSpec.server.libraries.title')}>
    {rows.map((row, index) => <li key={row.id} className={styles.row} data-library-row={row.id} data-drop-target={dropTarget === row.id || undefined}>
      <button type="button" className={styles.grip} aria-label={t('next.settings.reorder', {name: row.name})} disabled={!!busy}
        onKeyDown={event => {
          if (busy || !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const to = event.key === 'Home' ? 0 : event.key === 'End' ? libraries.length - 1 : index + (event.key === 'ArrowUp' ? -1 : 1);
          const library = libraries.find(value => value.id === row.id); if (library) void reorder(library, movedLibraryIds(libraries, row.id, to));
        }}
        onPointerDown={event => {
          if (event.button !== 0 || lifetime.current?.running) return;
          event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId);
          drag.current = {id: row.id, pointer: event.pointerId, to: index, libraries, api, service};
        }}
        onPointerMove={event => {
          const held = drag.current;
          if (!held || held.pointer !== event.pointerId) return;
          if (held.libraries !== libraries || held.api !== api || held.service !== service || lifetime.current?.running) {cancelDrag(); return;}
          const elements = [...(list.current?.querySelectorAll<HTMLElement>('[data-library-row]') ?? [])];
          const to = elements.findIndex(element => {const box = element.getBoundingClientRect(); return event.clientY >= box.top && event.clientY < box.bottom;});
          if (to >= 0) {held.to = to; setDropTarget(libraries[to].id);}
        }}
        onPointerUp={event => {
          const held = drag.current; cancelDrag();
          if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
          if (!held || held.pointer !== event.pointerId || held.libraries !== libraries || held.api !== api || held.service !== service) return;
          const library = libraries.find(value => value.id === held.id); if (library) void reorder(library, movedLibraryIds(libraries, held.id, held.to));
        }} onPointerCancel={cancelDrag} onLostPointerCapture={cancelDrag}><Icon name="i-grip" size={18} /></button>
      <span className={styles.kindIcon}><Icon name={kindIcons[row.kind]} size={22} /></span>
      <span className={styles.identity}>
        <b className={styles.name}>{row.name}</b>
        <small className={styles.folder}>{row.folder}</small>
        <small className={styles.lastScan}>{lastScanLabel(row.id)}</small>
      </span>
      {row.scanning
        ? <span className={styles.scanning} role="status"><Icon name="i-refresh" size={16} />{t('next.settings.libraries.scanning')}</span>
        : <Button variant="glass" size="small" disabled={!row.canScan || !!busy} loading={busy === row.id}
            onClick={() => { const library = libraries.find(value => value.id === row.id); if (library) void scan(library); }}>
            {t('next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.0.label')}
          </Button>}
      <Button variant="glass" size="small" disabled={!!busy} onClick={() => {if (!lifetime.current?.running) onEdit(row.id);}}>{t('next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.1.label')}</Button>
      <Menu open={menu === row.id} onOpenChange={open => {setAgentFact(undefined); setMenu(open ? row.id : undefined);}} title={row.name} items={[
        {id: 'edit', label: t('next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.1.label'), disabled: !!busy, onSelect: () => { if (!lifetime.current?.running) onEdit(row.id); }},
        ...(row.canScan && !row.scanning ? [{id: 'scan', label: t('next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.0.label'), onSelect: () => {
          const library = libraries.find(value => value.id === row.id); if (library) void scan(library);
        }}] : []),
        ...(onlineMenu && menu === row.id ? [{id: 'unmatched', label: t('next.settings.libraries.unmatched'), onSelect: () => {
          void router.navigate({to: '/library/$libraryId', params: {libraryId: row.id}, search: {unmatched: true}}).catch(onError);
        }}] : []),
        {id: 'up', label: t('next.settings.moveUp', {name: row.name}), disabled: !row.canMoveUp, onSelect: () => {
          const library = libraries.find(value => value.id === row.id); if (library) void move(library, -1);
        }},
        {id: 'down', label: t('next.settings.moveDown', {name: row.name}), disabled: !row.canMoveDown, onSelect: () => {
          const library = libraries.find(value => value.id === row.id); if (library) void move(library, 1);
        }},
        {id: 'remove', label: t('next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.label'), destructive: true,
          disabled: !!busy, onSelect: () => { if (!lifetime.current?.running) setRemoving(row.id); }},
      ]} trigger={<Button variant="icon" icon="i-more" iconOnly aria-label={t('next.settings.actions')} disabled={!!busy} />} />
    </li>)}
    </ol>
    <span className={styles.announcement} role="status">{announcement}</span>
    <AddLibrary open={adding} onOpenChange={setAdding} api={api} service={service} onError={onError} serverName={serverName} />
    <RemoveLibrary open={!!removing} onOpenChange={open => { if (!open) setRemoving(undefined); }} libraryId={removing ?? ''} service={service} api={api}
      onRemoved={() => { setRemoving(undefined); void service.loadLibraries().catch(onError); }} onError={onError} />
  </>;
}
