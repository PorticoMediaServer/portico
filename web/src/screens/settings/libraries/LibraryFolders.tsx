import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {ContentScope, LibraryContentApi} from '@core/library-content.ts';
import {LibraryInventoryService, type InventorySource} from '@core/library-inventory.ts';
import {Button, RichSelect, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {Menu} from '../../../kit/overlays/Popover';
import {FolderPicker} from './FolderPicker';
import {folderRows, folderIntervalOptions} from './model';
import styles from './libraries.module.css';

const t = defaultI18n.t;
const label = (id: string) => t(('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.' + id) as Parameters<typeof t>[0]);

/** The library page's Folders tab: where this library reads from, each folder's
 * kind and health, and the drawn folder menu — scan this folder, check
 * availability, edit and remove. Every command runs through the owner's own
 * inventory service and reconciles from the server's answer. */
export function LibraryFolders({libraryId, api, scope, isCurrent, onError, serverName}: Readonly<{
  libraryId: string; api: LibraryContentApi; scope: ContentScope; isCurrent: () => boolean; onError: (error: unknown) => void; serverName?: string;
}>) {
  const service = useMemo(() => new LibraryInventoryService({api, scope, libraryId}), [api, scope, libraryId]);
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const [menu, setMenu] = useState<string>(), [editing, setEditing] = useState<InventorySource>(), [removing, setRemoving] = useState<InventorySource>(), [busy, setBusy] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  useEffect(() => {setConfirmation('');}, [removing]);
  const [picking, setPicking] = useState(false), [adding, setAdding] = useState<string>();
  useEffect(() => { void service.refresh().catch(onError); return () => service.dispose(); }, [service, onError]);
  const lifetime = useRef<{running: boolean} | undefined>(undefined);
  useEffect(() => {
    const capture = {running: false}; lifetime.current = capture; setBusy(false);
    return () => {if (lifetime.current === capture) lifetime.current = undefined;};
  }, [service]);
  const run = async (command: () => Promise<boolean>, complete?: () => void) => {
    const capture = lifetime.current;
    if (!capture || capture.running || !isCurrent()) return;
    capture.running = true;
    const current = () => lifetime.current === capture && isCurrent();
    setBusy(true);
    try {
      const succeeded = await command();
      if (!current()) return;
      if (succeeded) complete?.();
      else onError(new Error('library_folder_failed'));
    } catch (error) { if (current()) onError(error); }
    finally { if (current()) {capture.running = false; setBusy(false);} }
  };
  if (!snapshot.config) return <div className={styles.formNote}>
    <p role={snapshot.loading ? 'status' : 'alert'}>{t(snapshot.loading ? 'next.settings.loading' : 'next.settings.folderFailed')}</p>
    {!snapshot.loading && <Button onClick={() => { if (isCurrent()) void service.refresh().catch(onError); }}>{t('next.states.tryAgain')}</Button>}
  </div>;
  const config = snapshot.config, rows = folderRows(config.sources);
  const source = (id: string) => config.sources.find(value => value.id === id);
  return <>
    {snapshot.error && <p role="alert" className={styles.formNote}>{t('next.settings.folderFailed')}</p>}
    <div className={styles.listHeader}>
      <Button variant="primary" size="small" icon="i-plus" disabled={busy} onClick={() => { if (!lifetime.current?.running) setPicking(true); }}>{t('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addLabel')}</Button>
    </div>
    {rows.length ? <ul className={styles.folders} aria-label={t('next.settingsSpec.server.libraries.panels.library.tabs.1.title')}>
      {rows.map(row => <li key={row.id} className={styles.folder} data-folder-row={row.id}>
        <span className={styles.folderName}>{row.name}</span>
        <small className={styles.folderPath}>{row.path}</small>
        <span className={styles.folderFacts}>
          <span>{t(row.network ? 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.1.label'
            : 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.0.label')}</span>
          {(!row.enabled || !/^(healthy|available|ok)$/i.test(row.health)) && <span className={styles.folderUnavailable}>{t('next.settings.libraries.folderUnavailable')}</span>}
        </span>
        <span className={styles.folderActions}>
          <Menu open={menu === row.id} onOpenChange={open => setMenu(open ? row.id : undefined)} title={row.name} items={[
            {id: 'scan', label: t('next.settings.libraries.scanFolder'), onSelect: () => { const found = source(row.id); if (found) void run(() => service.scanSource(found.id)); }},
            {id: 'check', label: t('next.settings.libraries.checkFolder'), onSelect: () => { const found = source(row.id); if (found) void run(() => service.checkSource(found.id)); }},
            {id: 'edit', label: t('next.settings.libraries.editFolder'), onSelect: () => { const found = source(row.id); if (found) setEditing(found); }},
            {id: 'remove', label: t('next.settings.libraries.removeFolderTitle'), destructive: true, onSelect: () => { const found = source(row.id); if (found) setRemoving(found); }},
          ]} trigger={<Button variant="icon" icon="i-more" iconOnly aria-label={t('next.settings.actions')} disabled={busy} />} />
        </span>
      </li>)}
    </ul> : <p className={styles.formNote}>{t('next.settings.libraries.noFolders')}</p>}
    <FolderPicker open={picking} onOpenChange={setPicking} api={api} onChoose={path => { setPicking(false); setAdding(path); }} serverName={serverName} />
    <FolderEditor api={api} serverName={serverName} open={!!adding} onOpenChange={open => { if (!open && !lifetime.current?.running) setAdding(undefined); }} initialPath={adding}
      busy={busy} onSave={edit => { void run(() => service.saveSource(undefined, {...edit, expectedRevision: config.revision, acceptReplacement: false}), () => setAdding(undefined)); }} />
    <FolderEditor api={api} serverName={serverName} open={!!editing} onOpenChange={open => { if (!open && !lifetime.current?.running) setEditing(undefined); }} source={editing}
      busy={busy} onSave={edit => { if (!editing) return;
        void run(() => service.saveSource(editing.id, {...edit, expectedRevision: config.revision, acceptReplacement: false}), () => setEditing(undefined)); }} />
    <Dialog open={!!removing} onOpenChange={open => { if (!open && !lifetime.current?.running) setRemoving(undefined); }} size="sm" role="alertdialog"
      title={t('next.settings.libraries.removeFolderTitle')} description={t('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.itemActions.1.confirm')}
      footer={<><Button disabled={busy} onClick={() => { if (!lifetime.current?.running) setRemoving(undefined); }}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={busy || !removing || confirmation !== removing.name} onClick={() => { if (!removing || confirmation !== removing.name) return; void run(() => service.removeSource(removing.id, config.revision), () => setRemoving(undefined)); }}>
          {t('next.settings.libraries.removeFolderTitle')}</Button></>}>
      <p className={styles.formNote}>{removing?.path}</p>
      <TextField label={t('confirm.typeToConfirm', {token: removing?.name ?? ''})} value={confirmation} disabled={busy}
        onChange={event => setConfirmation(event.currentTarget.value)} />
    </Dialog>
  </>;
}

/** One existing folder's editable facts: its name, its path, and how often the
 * server checks it for changes, with the real server folder explorer. */
function FolderEditor({api, serverName, open, onOpenChange, source, initialPath, busy, onSave}: Readonly<{
  api: LibraryContentApi; serverName?: string; open: boolean; onOpenChange: (open: boolean) => void; source?: InventorySource; initialPath?: string; busy: boolean;
  onSave: (edit: Readonly<{name: string; path: string; classification: 'local' | 'network'; followSymlinks: boolean; intervalSeconds: number}>) => void;
}>) {
  const [choosing, setChoosing] = useState(false);
  useEffect(() => {if (!open) setChoosing(false);}, [open]);
  const [classification, setClassification] = useState<'local' | 'network'>('local');
  const [followSymlinks, setFollowSymlinks] = useState(false);
  const [name, setName] = useState(''), [path, setPath] = useState(''), [interval, setInterval] = useState('0');
  useEffect(() => {
    setClassification(source?.classification ?? 'local'); setFollowSymlinks(source?.followSymlinks ?? false);
    if (source) { setName(source.name); setPath(source.path); setInterval(String(source.intervalSeconds)); return; }
    if (initialPath) { const parts = initialPath.split(/[\\/]/).filter(Boolean); setName(parts[parts.length - 1] ?? ''); setPath(initialPath); setInterval('0'); }
  }, [source, initialPath]);
  return <><Dialog open={open} onOpenChange={next => { if (!busy) onOpenChange(next); }} size="md" title={t(source ? 'next.settings.libraries.editFolder' : 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addLabel')}
    footer={<><Button disabled={busy} onClick={() => { if (!busy) onOpenChange(false); }}>{t('next.settings.cancel')}</Button>
      <Button variant="primary" loading={busy} disabled={busy || !name.trim() || !path.trim()} onClick={() => {
        onSave({name: name.trim(), path: path.trim(), classification,
          followSymlinks, intervalSeconds: Number(interval) || 0});
      }}>{t('next.settings.libraries.saveFolder')}</Button></>}>
    <div className={styles.form}>
      <TextField label={label('1.rows.1.label')} value={name} onChange={event => setName(event.currentTarget.value)} />
      <TextField label={label('1.rows.0.label')} value={path} onChange={event => setPath(event.currentTarget.value)} />
      <Button disabled={busy} onClick={() => setChoosing(true)}>{t('next.settings.chooseFolder')}</Button>
      <RichSelect label={t('next.ownerLibraries.addFolder.location')} value={classification}
        options={(['local', 'network'] as const).map((value, index) => ({value, label: t(('next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.' + index + '.label') as Parameters<typeof t>[0])}))}
        onValueChange={value => { if (!busy && (value === 'local' || value === 'network')) setClassification(value); }} />
      <label><input type="checkbox" checked={followSymlinks} disabled={busy} onChange={event => setFollowSymlinks(event.currentTarget.checked)} /> {t('next.ownerLibraries.addFolder.follow')}</label>
      <RichSelect label={label('1.rows.3.label')} value={interval} options={folderIntervalOptions().map(option => ({...option, label: t(option.label)}))}
        onValueChange={setInterval} />
    </div>
  </Dialog>
  <FolderPicker open={open && choosing} onOpenChange={setChoosing} api={api} onChoose={chosen => {setPath(chosen);setChoosing(false);}} serverName={serverName} />
  </>;
}
