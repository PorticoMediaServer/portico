import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LibraryManagementService} from '@core/library-management.ts';
import type {ShellArtwork} from '../../../shell/artwork';
import {Artwork} from '../../../kit/artwork/Artwork';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays/Popover';
import {RenameLibrary} from './RenameLibrary';
import {LibraryScanAction} from './LibraryActions';
import {useLibraryLyricsFetch} from './use-lyrics-fetch';
import styles from './library-header.module.css';

const pivots: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', artist: 'artists', album: 'albums', song: 'songs', book: 'books'};
const neverCurrent = () => false;
/** Only the selected server library's bounded summary supplies counts and art. */
export function LibraryHeader({libraryId, name, service, artwork, onError, rename, lyrics}: Readonly<{
  rename: (name: string) => Promise<boolean>; libraryId: string; name: string; service: LibraryManagementService; artwork: ShellArtwork; onError: (error: unknown) => void;
  /** The library's ⋯ menu carries Fetch missing lyrics for music only, through the
   * same command and lifetime fence as the Metadata tab row. */
  lyrics?: Readonly<{api: LibraryContentApi; available: boolean; isCurrent: () => boolean}>;
}>) {
  const [menu, setMenu] = useState(false), [renameRequest, setRenameRequest] = useState(0), [busy, setBusy] = useState(false);
  const lyric = useLibraryLyricsFetch({api: lyrics?.api, libraryId, isCurrent: lyrics?.isCurrent ?? neverCurrent, onError});
  const lifetime = useRef<{running: boolean} | undefined>(undefined);
  useEffect(() => {const capture = {running: false}; lifetime.current = capture; return () => {if (lifetime.current === capture) lifetime.current = undefined;};}, [libraryId, service]);
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  useEffect(() => {
    if (service.getSnapshot().selectedLibraryId !== libraryId) void service.selectLibrary(libraryId).catch(onError);
  }, [service, libraryId, onError]);
  const library = snapshot.selected.data?.library.id === libraryId ? snapshot.selected.data.library : snapshot.directory.data?.items.find(item => item.id === libraryId);
  const square = library?.kind === 'music' || library?.kind === 'audiobook';
  const scan = library?.lastScan;
  const active = scan && ['queued', 'running', 'paused'].includes(scan.status);
  const fullScan = async () => {
    const capture = lifetime.current, state = service.getSnapshot();
    if (!capture || capture.running || state.mutation.phase === 'pending' || state.selected.phase !== 'ready' || state.selected.data?.library.id !== libraryId) return;
    capture.running = true; setBusy(true);
    try {
      await service.scan('full');
      if (lifetime.current === capture && service.getSnapshot().mutation.phase !== 'complete') throw new Error('library_scan_failed');
    } catch (error) {if (lifetime.current === capture) onError(error);}
    finally {if (lifetime.current === capture) {capture.running = false;setBusy(false);}}
  };
  const disabled = busy || snapshot.mutation.phase === 'pending' || snapshot.selected.phase !== 'ready';
  return <header className={styles.hero}>
    <div className={styles.strip} data-square={square || undefined} aria-hidden="true">
      {library?.summary?.coverUrls.map((path, order) => {
        const source = artwork.foreground(path, 240, square ? 1 : 2 / 3, {group: 'settings-library', order});
        return source && <span key={path}><Artwork source={source} alt="" visible /></span>;
      })}
    </div>
    <div className={styles.content}>
      <h1>{name}</h1>
      <div className={styles.facts}>
        {library?.summary?.counts.map(count => <span key={count.kind}>{defaultI18n.t(count.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: count.count, pivot: pivots[count.kind] ?? count.kind})}</span>)}
        {active ? <span role="status">{defaultI18n.t('next.settings.libraries.scanning')}</span> : scan?.finishedAt && Number.isFinite(Date.parse(scan.finishedAt)) && ['complete', 'complete_with_warnings'].includes(scan.status)
          ? <span>{defaultI18n.t('server.libraries.scanned', {when: defaultI18n.dateTime(scan.finishedAt)})}</span> : null}
      </div>
      {library?.actions.includes('scan') && <div className={styles.actions}><LibraryScanAction rowId="libraries.scanNow" label={defaultI18n.t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.0.control.label')} libraryId={libraryId} service={service} onError={onError} />
        <Menu open={menu} onOpenChange={setMenu} title={name} items={[
          ...(library.actions.includes('rename') ? [{id: 'rename', label: defaultI18n.t('next.ownerLibraries.rename'), disabled, onSelect: () => {if (!lifetime.current?.running) setRenameRequest(value => value + 1);}}] : []),
          {id: 'full', label: defaultI18n.t('next.ownerLibraries.scanAll'), disabled: disabled || !!active, onSelect: () => {void fullScan();}},
          ...(library?.kind === 'music' && lyrics ? [{id: 'lyrics', label: defaultI18n.t('web.libraries.fetchLyrics'), disabled: disabled || !lyrics.available || lyric.busy, onSelect: () => {void lyric.run();}}] : []),
        ]} trigger={<Button variant="icon" icon="i-more" iconOnly disabled={disabled} aria-label={defaultI18n.t('next.ownerLibraries.libraryMenu')} />} />
        <RenameLibrary name={name} disabled={disabled} rename={rename} request={renameRequest} hideTrigger />
      </div>}
    </div>
  </header>;
}
