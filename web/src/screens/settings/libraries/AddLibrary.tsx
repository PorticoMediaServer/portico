import {useCallback, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LibraryManagementService, ManagedLibrary} from '@core/library-management.ts';
import {Button, RichSelect, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {FolderPicker, ChosenServerFolders} from './FolderPicker';
import {localOnlyKind, metadataAgentFor, libraryKindLabel} from './model';
import styles from './libraries.module.css';

const t = defaultI18n.t;
type Kind = ManagedLibrary['kind'];

/** The seven drawn kinds, in the order Portico lists them (owner-libraries.html#8),
 * with the frame's own names and each kind's authored description. */
const KINDS: readonly {id: Kind; hint: string}[] = [
  {id: 'movie', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.0.hint')},
  {id: 'tv', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.hint')},
  {id: 'anime', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.2.hint')},
  {id: 'music', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.3.hint')},
  {id: 'audiobook', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.4.hint')},
  {id: 'book', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.5.hint')},
  {id: 'comic', hint: t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.6.hint')},
];


export function AddLibrary({open, onOpenChange, api, service, onError, initialKind = 'movie', serverName}: Readonly<{
  initialKind?: Kind; open: boolean; onOpenChange: (open: boolean) => void; api: LibraryContentApi; service: LibraryManagementService; onError: (error: unknown) => void; serverName?: string;
}>) {
  const [kind, setKind] = useState<Kind>(initialKind), [name, setName] = useState(libraryKindLabel(initialKind)), [touched, setTouched] = useState(false);
  const [paths, setPaths] = useState<readonly string[]>([]);
  const [source, setSource] = useState<'online' | 'local'>('online');
  const [picking, setPicking] = useState(false), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const submitting = useRef(false);
  const choosePaths = useCallback((next: readonly string[]) => {setPaths(Object.freeze([...next]));}, []);
  const choosePath = useCallback((path: string) => choosePaths([path]), [choosePaths]);
  const choose = (next: Kind) => { setKind(next); if (!touched) setName(libraryKindLabel(next)); };
  const create = async () => {
    if (submitting.current || !open || !name.trim() || !paths.length) return;
    submitting.current = true;
    setBusy(true); setFailed(false);
    try {
      await service.createLibrary({name: name.trim(), kind, folders: paths, metadataAgent: metadataAgentFor(kind) === 'local' ? 'local' : source});
      if (service.getSnapshot().mutation.phase !== 'complete') {setFailed(true);return;}
      onOpenChange(false); setName(libraryKindLabel('movie')); setPaths([]); setKind('movie'); setTouched(false); setSource('online');
    } catch (error) { setFailed(true); onError(error); }
    finally {submitting.current = false; setBusy(false);}
  };
  return <>
    <Dialog open={open} onOpenChange={value => {if (!submitting.current) onOpenChange(value);}} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showCloseButton={!busy} size="md" title={t('next.settingsSpec.server.libraries.panels.newLibrary.title')}
      footer={<><Button disabled={busy} onClick={() => {if (!submitting.current) onOpenChange(false);}}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={busy || !name.trim() || !paths.length} onClick={() => { void create(); }}>{t('next.settingsSpec.server.libraries.panels.newLibrary.footer.primary')}</Button></>}>
      <div className={styles.form}>
        <RichSelect label={t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.label')}
          value={kind} options={KINDS.map(value => ({value: value.id, label: libraryKindLabel(value.id), description: value.hint}))}
          onValueChange={value => { const next = KINDS.find(item => item.id === value); if (next) choose(next.id); }} />
        <TextField label={t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.0.label')} value={name}
          placeholder={t('next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.0.control.placeholder')}
          onChange={event => { setTouched(true); setName(event.currentTarget.value); }} />
        <div className={styles.formFolder}>
          <Button disabled={busy} onClick={() => setPicking(true)}>{t('next.ownerLibraries.explorer.choose')}</Button>
          {!paths.length ? <p className={styles.formNote}>{t('next.ownerLibraries.addLibrary.foldersHelp')}</p> : null}
        </div>
        {paths.length ? <ChosenServerFolders paths={paths} disabled={busy} onRemove={value => {if (!submitting.current) setPaths(previous => previous.filter(item => item !== value));}}/> : null}
        {localOnlyKind(kind)
          ? <p className={styles.formNote}>{t('next.settings.libraries.localOnly')}</p>
          : <RichSelect label={t('next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.label')} value={source}
            options={[{value: 'online', label: t('next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.0.label'),
              description: t('next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.0.hint')},
              {value: 'local', label: t('next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.1.label'),
                description: t('next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.1.hint')}]}
            onValueChange={value => setSource(value === 'local' ? 'local' : 'online')} />}
        {failed && <p role="alert">{t('next.settings.libraries.createFailed')}</p>}
      </div>
    </Dialog>
    <FolderPicker open={picking} onOpenChange={setPicking} api={api} initialPaths={paths} onChoose={choosePath} onChooseMany={choosePaths} serverName={serverName} />
  </>;
}
