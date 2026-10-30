import {Fragment, useEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {DownloadAction, DownloadPreparation, DownloadTitle} from '@core/downloads.ts';
import type {DownloadsService, DownloadsSnapshot} from '@core/downloads-service.ts';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Button, Select, Switch} from '../../kit/controls';
import {Dialog, Menu, type MenuEntry} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {StateView} from '../../kit/states';
import {sameSavedDownloadsScope, type SavedDownloadsPort, type SavedDownloadRules, type SavedDownloadsSnapshot, type SavedLocalDownload, type SavedLocalDownloadGroup, type SavedOtherDownloadsConfirmation} from './downloads-port.ts';
import {diskFractions, downloadShape, localDownloadGroups, localGroupPercent, measuredPercent, preparationPercent} from './downloads-model.ts';
import type {SavedOperations} from './operations.ts';
import type {SaveReadyPreparation} from './ready-downloads.ts';
import {savedText as s} from './text.ts';
import {SavedDialog} from './SavedDialog';
import styles from './saved.module.css';

const t = defaultI18n.t;
export type SavedDownloadEffects = Readonly<{
  openItem(itemId: string): void;
  /** The parent supplies its real selected-authority browser file handoff. */
  saveFile(preparation: DownloadPreparation, signal: AbortSignal): Promise<void>;
  /** Optional native handoff, closed over this runtime's preparation service and
   * genuine authenticated coordinator by the parent. Never a browser store. */
  saveReadyPreparation?: SaveReadyPreparation;
  reportError(error: unknown): void;
  openServerSettings?: () => void;
}>;
type ArtworkForPreparation = (preparation: DownloadPreparation) => ArtworkSource | undefined;

function DownloadFacts({title, quality, bytes, browser = false}: {title: DownloadTitle; quality?: string; bytes?: number; browser?: boolean}) {
  const episode = title.kind === 'episode' && title.number !== null ? title.groupNumber === null
    ? t('title.episodeOnly', {episode: title.number}) : t('next.kit.episodeCode', {season: title.groupNumber, episode: title.number}) : undefined;
  const kind = browser && title.kind === 'movie' ? t('search.kind.movie') : undefined;
  const qualitySize = browser && quality && bytes !== undefined && bytes > 0 ? s('next.saved.qualitySize', {quality, size: defaultI18n.bytes(bytes)}) : undefined;
  const facts = [browser && title.kind === 'episode' ? title.parentTitle : kind, episode,
    title.kind === 'song' || title.kind === 'album' ? title.artist : title.kind !== 'episode' && title.year ? defaultI18n.number(title.year, {useGrouping: false}) : undefined,
    qualitySize ?? quality, !qualitySize && bytes !== undefined && bytes > 0 ? defaultI18n.bytes(bytes) : undefined].filter((fact): fact is string => !!fact);
  return <small className={styles.facts}>{facts.map((fact, index) => <span key={index}>{fact}</span>)}</small>;
}
function Progress({percent}: {percent: number | undefined}) {
  return percent === undefined ? null : <span className={styles.progress} aria-hidden="true"><span style={{width: `${percent}%`}} /></span>;
}
function DownloadMenu({items}: {items: readonly MenuEntry[]}) {
  const [open, setOpen] = useState(false);
  return <Menu open={open} onOpenChange={setOpen} title={t('player.more')} accessibilityLabel={t('player.more')} width={240} items={items}
    trigger={<Button className={styles.more} variant="icon" size="small" icon="i-more" iconOnly aria-label={t('player.more')} />} />;
}

function NativeArtwork({id, revision, title, port}: {id: string; revision: number | string; title: DownloadTitle; port: SavedDownloadsPort}) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    const controller = new AbortController(); let retained: string | undefined;
    setUrl(undefined);
    void port.getArtwork(id, controller.signal).then(blob => {
      if (controller.signal.aborted || !blob) return;
      retained = URL.createObjectURL(blob); setUrl(retained);
    }).catch(() => {});
    return () => { controller.abort(); if (retained) URL.revokeObjectURL(retained); };
  }, [port, id, revision]);
  return <span className={styles.art} data-shape={downloadShape(title)}>{url && <img src={url} alt="" />}</span>;
}

function LocalStatus({item, items}: {item: SavedLocalDownload; items: readonly SavedLocalDownload[]}) {
  const percent = measuredPercent(item.percent), waitingFor = items.find(row => row.id === item.waitsForId);
  const checkIn = item.state === 'ready' && item.offlineAccess === 'needsCheckIn';
  const label = checkIn ? s('next.saved.checkIn') : !item.onServer ? s('next.saved.gone') : item.state === 'ready' ? s('next.saved.downloaded')
    : item.state === 'downloading' ? percent === undefined ? s('next.saved.downloadingUnknown') : s('next.saved.downloading', {percent: Math.floor(percent)})
      : item.state === 'failed' ? s('next.saved.stopped') : item.state === 'paused' ? t('downloads.paused') : t('web.downloads.state.waiting');
  return <span className={styles.status}>
    <b data-tone={checkIn ? 'warning' : item.state === 'failed' ? 'error' : item.state === 'ready' && item.onServer ? 'healthy' : undefined}>
      {item.state === 'ready' && item.onServer && !checkIn && <Icon name="i-check" size={16} />}{label}
    </b>
    {!checkIn && !item.onServer ? <small>{s('next.saved.keptOnComputer')}</small> : item.state === 'failed' && percent !== undefined ? <small>{t('server.plays.stoppedAt', {percent: Math.floor(percent)})}</small>
      : item.state === 'queued' && waitingFor ? <small>{s('next.saved.startsAfter', {title: waitingFor.title.title})}</small>
        : item.state === 'downloading' && item.etaSeconds !== undefined && Number.isFinite(item.etaSeconds) && item.etaSeconds >= 0 ? <small>{t('web.downloads.minutesLeft', {count: Math.max(1, Math.ceil(item.etaSeconds / 60))})}</small> : null}
    {item.state === 'downloading' && <Progress percent={percent} />}
  </span>;
}

function LocalGroupStatus({group}: {group: SavedLocalDownloadGroup}) {
  const percent = localGroupPercent(group), checkIn = group.state === 'ready' && group.offlineAccess === 'needsCheckIn';
  const label = checkIn ? s('next.saved.checkIn') : group.state === 'ready' ? s('next.saved.downloaded')
    : group.state === 'failed' ? s('next.saved.stopped') : group.state === 'paused' ? t('downloads.paused')
      : group.state === 'queued' ? t('web.downloads.state.waiting') : percent === undefined ? s('next.saved.downloadingUnknown') : s('next.saved.downloading', {percent: Math.floor(percent)});
  return <span className={styles.status}>
    <b data-tone={checkIn ? 'warning' : group.state === 'failed' ? 'error' : group.state === 'ready' ? 'healthy' : undefined}>
      {group.state === 'ready' && !checkIn && <Icon name="i-check" size={16} />}{label}
    </b>
    {group.state === 'failed' && percent !== undefined && <small>{t('server.plays.stoppedAt', {percent: Math.floor(percent)})}</small>}
    {group.state === 'downloading' && <Progress percent={percent} />}
  </span>;
}

type LocalConfirmation = Readonly<{kind: 'item'; item: SavedLocalDownload}> | Readonly<{kind: 'others'; value: SavedOtherDownloadsConfirmation}> | Readonly<{kind: 'group'; group: SavedLocalDownloadGroup; title: string}>;
export function LocalDownloads({port, operations, effects, server}: {port: SavedDownloadsPort; operations: SavedOperations; effects: SavedDownloadEffects; server?: ReactNode}) {
  const snapshot = useSyncExternalStore(port.subscribe, port.getSnapshot, port.getSnapshot);
  const reportError = effects.reportError;
  const [busy, setBusy] = useState<string>(), [confirmation, setConfirmation] = useState<LocalConfirmation>(), [error, setError] = useState(false);
  const pendingAction = useRef(false);
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const controller = new AbortController();
    void port.refresh(controller.signal).catch(error => { if (!controller.signal.aborted && operations.current) reportError(error); });
    return () => controller.abort();
  }, [port, operations, reportError]);
  const perform = <T,>(key: string, action: (signal: AbortSignal) => Promise<T>, settled?: (value: T) => void) => {
    if (pendingAction.current || !operations.current) return;
    pendingAction.current = true; setBusy(key); setError(false);
    void operations.run(action, value => { pendingAction.current = false; setBusy(undefined); settled?.(value); }).catch(error => {
      pendingAction.current = false; setBusy(undefined); setError(true); effects.reportError(error);
    });
  };
  if (!operations.current || !snapshot.available) return null;
  const groups = localDownloadGroups(snapshot.items);
  return <div className={styles.downloadColumns} data-mode="local">
    <div>
      {snapshot.phase === 'error' && !snapshot.items.length ? <StateView title={t('downloads.openFailed')} primary={{id: 'retry', label: t('action.tryAgain'), onPress: () => perform('refresh', signal => port.refresh(signal))}} /> : null}
      {snapshot.phase === 'ready' && !snapshot.items.length && <StateView title={t('downloads.emptyTitle')} body={t('downloads.emptyBody')} />}
      {groups.map(group => {
        const count = group.kind === 'show' ? snapshot.groups?.filter(total => total.kind === 'episodes' && total.parentId === group.id.slice(5)).reduce((sum, total) => sum + total.count, 0) : undefined;
        return <section key={group.id}>
          {group.kind !== 'other' && <div className={styles.downloadHeading}><h2>{group.title ?? (group.kind === 'server' ? t('downloads.onServer') : s(`next.saved.${group.kind === 'show' ? 'movies' : group.kind}`))}</h2>
            {count !== undefined && count > 0 && <span>{t('card.episodesAired', {count})}</span>}
          </div>}
          <div className={styles.downloadList} role="list">
            {group.items.map(item => {
              const allAlbumGroups = group.kind === 'music' && item.title.kind === 'song' ? snapshot.groups?.filter(value => value.kind === 'songs' && value.parentId === item.title.parentId) : undefined;
              const album = allAlbumGroups?.length === 1 && allAlbumGroups[0].state && allAlbumGroups[0].parentTitle ? allAlbumGroups[0] : undefined;
              const albumTitle = album?.parentTitle;
              if (album && group.items.find(value => value.title.parentId === album.parentId) !== item) return null;
              if (album && albumTitle) {
                const selector = {kind: album.kind, parentId: album.parentId, ...(album.groupId ? {groupId: album.groupId} : {})};
                return <div key={`album:${album.parentId}:${album.groupId}`} className={styles.downloadRow} role="listitem">
                  <NativeArtwork id={album.artworkId ?? item.id} revision={album.revision} title={item.title} port={port} />
                  <div className={styles.title}><b>{albumTitle}</b><small className={styles.facts}>{album.artist && <span>{album.artist}</span>}<span>{t('next.kit.songs', {count: album.count})}</span>{album.qualityLabel && <span>{album.qualityLabel}</span>}{album.bytes > 0 && <span>{defaultI18n.bytes(album.bytes)}</span>}</small></div>
                  <LocalGroupStatus group={album} />
                  <span className={styles.actions}>
                    {album.state === 'ready' && album.held === false && album.offlineAccess === 'valid' && port.playGroup && <Button className={styles.rowButton} variant="primary" size="small" icon="i-play" loading={busy === album.revision} onClick={() => perform(album.revision, signal => port.playGroup!(selector, album.revision, signal))}>{t('action.play')}</Button>}
                    {album.state === 'downloading' && <Button className={styles.rowButton} size="small" icon="i-pause" loading={busy === album.revision} onClick={() => perform(album.revision, signal => port.actionGroup(selector, album.revision, 'pause', signal))}>{t('action.pause')}</Button>}
                    {album.state === 'paused' && album.held === false && <Button className={styles.rowButton} size="small" icon="i-play" loading={busy === album.revision} onClick={() => perform(album.revision, signal => port.actionGroup(selector, album.revision, 'resume', signal))}>{t('entry.resume')}</Button>}
                    {album.state === 'failed' && album.held === false && <Button className={styles.rowButton} size="small" icon="i-retry" loading={busy === album.revision} onClick={() => perform(album.revision, signal => port.actionGroup(selector, album.revision, 'retry', signal))}>{t('action.tryAgain')}</Button>}
                    <DownloadMenu items={[{id: 'go', label: t('web.downloads.goToTitle'), onSelect: () => { if (operations.current) effects.openItem(album.parentId); }}, {id: 'remove', label: t('downloads.removeFromDevice'), destructive: true, disabled: !!busy, onSelect: () => setConfirmation({kind: 'group', group: album, title: albumTitle})}]} />
                  </span>
                </div>;
              }
              return <div key={item.id} className={styles.downloadRow} role="listitem" data-download-id={item.id}>
              <NativeArtwork id={item.id} revision={item.revision} title={item.title} port={port} />
              <div className={styles.title}><b>{item.title.title}</b><DownloadFacts title={item.title} quality={item.qualityLabel} bytes={item.bytes} /></div>
              <LocalStatus item={item} items={snapshot.items} />
              <span className={styles.actions}>
                {item.state === 'ready' && !item.held && item.offlineAccess === 'valid' && <Button className={styles.rowButton} variant="primary" size="small" icon="i-play" loading={busy === item.id} onClick={() => perform(item.id, signal => port.play(item.id, signal))}>{t('action.play')}</Button>}
                {item.state === 'downloading' && <Button className={styles.rowButton} size="small" icon="i-pause" loading={busy === item.id} onClick={() => perform(item.id, signal => port.action(item.id, item.revision, 'pause', signal))}>{t('action.pause')}</Button>}
                {item.state === 'paused' && !item.held && <Button className={styles.rowButton} size="small" icon="i-play" loading={busy === item.id} onClick={() => perform(item.id, signal => port.action(item.id, item.revision, 'resume', signal))}>{t('entry.resume')}</Button>}
                {item.state === 'failed' && !item.held && <Button className={styles.rowButton} size="small" icon="i-retry" loading={busy === item.id} onClick={() => perform(item.id, signal => port.action(item.id, item.revision, 'retry', signal))}>{t('action.tryAgain')}</Button>}
                <DownloadMenu items={[...(item.onServer ? [{id: 'go', label: t('web.downloads.goToTitle'), onSelect: () => { if (operations.current) effects.openItem(item.itemId); }}] : []),
                  {id: 'remove', label: t('downloads.removeFromDevice'), destructive: true, disabled: !!busy, onSelect: () => setConfirmation({kind: 'item', item})}]} />
              </span>
            </div>;})}
          </div>
        </section>;
      })}
      {snapshot.nextCursor && <Button className={styles.showMore} size="small" loading={busy === 'more'} onClick={() => perform('more', signal => port.loadMore(signal))}>{t('action.showMore')}</Button>}
      {server}
    </div>
    <aside className={styles.downloadSide}>
      <DownloadStorage snapshot={snapshot} />
      {snapshot.rules && <DownloadRules snapshot={snapshot} busy={busy !== undefined} onChange={value => perform('rules', signal => port.setRules(snapshot.rules!.revision, value, signal))} onChooseFolder={() => perform('folder', signal => port.chooseFolder(signal))} />}
      {!!snapshot.storage?.otherDownloadsBytes && <div className={styles.otherDownloads}><Icon name="i-info" size={18} /><span>{s('next.saved.otherProfiles', {size: defaultI18n.bytes(snapshot.storage.otherDownloadsBytes)})}</span>
        <Button size="xs" loading={busy === 'others'} onClick={() => perform('others', async signal => {
          const value = await port.previewOtherProfiles(signal);
          if (!sameSavedDownloadsScope(value.scope, port.scope)) throw new DOMException('The download scope changed.', 'AbortError');
          return value;
        }, value => setConfirmation({kind: 'others', value}))}>{t('action.removeShort')}</Button>
      </div>}
      {error && <p role="alert">{t('downloads.settingFailed')}</p>}
    </aside>
    <Dialog open={!!confirmation} onOpenChange={open => { if (!open && !busy) setConfirmation(undefined); }} initialFocusRef={cancel}
      title={t(confirmation?.kind === 'others' ? 'confirm.removeOthers.title' : 'confirm.removeDownload.title')}
      description={confirmation?.kind === 'others' ? t('confirm.removeOthers.body') : t('confirm.removeDownload.body', {title: confirmation?.kind === 'item' ? confirmation.item.title.title : confirmation?.title ?? ''})}
      footer={<><Button ref={cancel} size="small" onClick={() => setConfirmation(undefined)} disabled={!!busy}>{t('action.cancel')}</Button><Button className={styles.destructive} size="small" loading={!!busy} onClick={() => {
        if (!confirmation) return;
        if (confirmation.kind === 'others') perform('others-remove', signal => port.removeOtherProfiles(confirmation.value, signal), () => setConfirmation(undefined));
        else if (confirmation.kind === 'group') {
          const group = confirmation.group;
          perform(group.revision, signal => port.actionGroup({kind: group.kind, parentId: group.parentId, ...(group.groupId ? {groupId: group.groupId} : {})}, group.revision, 'remove', signal), () => setConfirmation(undefined));
        }
        else perform(confirmation.item.id, signal => port.action(confirmation.item.id, confirmation.item.revision, 'remove', signal), () => setConfirmation(undefined));
      }}>{t('action.removeShort')}</Button></>}><Fragment /></Dialog>
  </div>;
}

function DownloadStorage({snapshot}: {snapshot: SavedDownloadsSnapshot}) {
  const storage = snapshot.storage, fractions = storage && diskFractions(storage);
  if (!storage) return null;
  return <div className={styles.storage}>
    <div className={styles.storageHeading}><b>{t('downloads.used', {size: defaultI18n.bytes(storage.usedBytes)})}</b><span>{s('next.saved.free', {size: defaultI18n.bytes(storage.freeBytes)})}</span></div>
    {fractions && <><div className={styles.storageBar} aria-hidden="true"><span style={{width: `${fractions.portico * 100}%`}} /><span style={{width: `${fractions.other * 100}%`}} /></div>
      <div className={styles.storageKey}><span>{s('next.saved.portico')}</span><span>{s('next.saved.otherApps')}</span></div></>}
  </div>;
}
function DownloadRules({snapshot, busy, onChange, onChooseFolder}: {snapshot: SavedDownloadsSnapshot; busy: boolean; onChange: (value: SavedDownloadRules) => void; onChooseFolder: () => void}) {
  if (!snapshot.rules) return null;
  const value = snapshot.rules.value;
  return <div className={styles.rules}>
    <h3>{t('downloads.rules')}</h3>
    {([
      ['removeWatched', t('downloads.removeAfterWatching'), t('downloads.removeAfterWatchingHelp')],
      ['nextEpisode', t('downloads.nextEpisode'), t('downloads.nextEpisodeHelp')],
      ['unmeteredOnly', s('next.saved.unmetered'), s('next.saved.unmeteredHelp')],
    ] as const).map(([field, label, help]) => <label className={styles.rule} key={field}><span>{label}<small>{help}</small></span><Switch checked={value[field]} disabled={busy} onCheckedChange={checked => onChange({...value, [field]: checked})} /></label>)}
    <div className={styles.rule}><span>{t('downloads.storageLimit')}<small>{s('next.saved.limitHelp')}</small></span>
      <Select value={String(value.limitBytes)} disabled={busy} aria-label={t('downloads.storageLimit')} className={styles.limitSelect}
        options={[0, 5, 10, 25, 50, 100].map(gb => ({value: String(gb * 1024 ** 3), label: gb ? defaultI18n.bytes(gb * 1024 ** 3) : t('downloads.noLimit')}))}
        onValueChange={limit => onChange({...value, limitBytes: Number(limit)})} />
    </div>
    {snapshot.folder && <div className={styles.rule}><span>{s('next.saved.folder')}<code className={styles.folderPath}>{snapshot.folder.displayPath}</code></span><Button size="xs" disabled={busy} onClick={onChooseFolder}>{s('next.saved.changeFolder')}</Button></div>}
  </div>;
}

export function ServerDownloads({service, operations, effects, artwork, online, embedded = false, managed = false}: {service: DownloadsService; operations: SavedOperations; effects: SavedDownloadEffects; artwork: ArtworkForPreparation; online: boolean; embedded?: boolean; managed?: boolean}) {
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const [qualities, setQualities] = useState<ReadonlyMap<string, string>>(new Map()), [confirmation, setConfirmation] = useState<Readonly<{item: DownloadPreparation; action: 'remove' | 'cancel'}>>();
  const [dismissedFailure, setDismissedFailure] = useState<DownloadsSnapshot>();
  const pendingSaves = useRef(new Set<string>()), [saving, setSaving] = useState<readonly string[]>([]);
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!online || !operations.current) return;
    if (!managed) service.start(); const controller = new AbortController();
    void service.qualities(controller.signal).then(choices => { if (!controller.signal.aborted && operations.current) setQualities(new Map(choices.map(choice => [choice.quality, choice.label]))); }).catch(() => {});
    return () => { controller.abort(); if (!managed) service.stop(); };
  }, [service, operations, online, managed]);
  const act = (item: DownloadPreparation, action: DownloadAction) => {
    if (!online || !operations.current || service.getSnapshot().busy.includes(item.id)) return;
    void operations.run(async () => {
      await service.act(item, action);
      const failure = service.getSnapshot().failure;
      if (failure) throw failure;
    }, () => setConfirmation(undefined)).catch(effects.reportError);
  };
  const saveFile = (item: DownloadPreparation) => {
    if (!online || !operations.current || item.state !== 'ready' || pendingSaves.current.has(item.id)) return;
    pendingSaves.current.add(item.id); setSaving([...pendingSaves.current]);
    void operations.run(async signal => {
      try { await effects.saveFile(item, signal); }
      finally { pendingSaves.current.delete(item.id); }
    }, () => setSaving([...pendingSaves.current])).catch(error => {
      setSaving([...pendingSaves.current]); effects.reportError(error);
    });
  };
  if (!operations.current) return null;
  if (snapshot.unavailable) return embedded ? null : <StateView title={t('web.downloads.offTitle')} body={t(effects.openServerSettings ? 'web.downloads.offOwner' : 'web.downloads.offMember')}
    primary={effects.openServerSettings ? {id: 'settings', label: s('next.saved.openServerSettings'), onPress: effects.openServerSettings} : undefined} />;
  return <div className={styles.serverDownloads} data-embedded={embedded || undefined}>
    <div className={styles.downloadHeading}><h2>{embedded ? t('downloads.onServer') : s('next.saved.prepared')}</h2>{!embedded && snapshot.usage && <span>{t('web.downloads.usage', {size: defaultI18n.bytes(snapshot.usage.profileBytes)})}</span>}</div>
    {!embedded && <p className={styles.browserNote}><Icon name="i-info" size={18} />{s('next.saved.browserNote')}</p>}
    {snapshot.phase === 'ready' && !snapshot.items.length && !snapshot.requests.length && !embedded && <StateView title={t('web.downloads.emptyTitle')} body={t('web.downloads.emptyBody')} />}
    <SavedDialog width={380} open={!embedded && snapshot.phase === 'error' && snapshot !== dismissedFailure} onOpenChange={open => { if (!open) setDismissedFailure(snapshot); }} title={s('next.saved.loadFailed')} description={s('next.saved.loadFailedBody')}
      footer={<><Button size="small" onClick={() => setDismissedFailure(snapshot)}>{t('action.close')}</Button><Button size="small" variant="primary" disabled={!online} onClick={() => { setDismissedFailure(snapshot); void service.refresh(); }}>{t('action.tryAgain')}</Button></>}>
      <Fragment />
    </SavedDialog>
    <div className={styles.downloadList} role="list">{snapshot.items.map(item => <div key={item.id} className={styles.downloadRow} role="listitem">
      <span className={styles.art} data-shape={downloadShape(item.title)}><Artwork source={artwork(item)} alt="" /></span>
      <div className={styles.title}><b>{item.title.title}</b><DownloadFacts title={item.title} quality={item.quality === 'original' ? t('web.downloads.original') : qualities.get(item.quality)} bytes={item.artifact.bytes} browser /></div>
      <ServerStatus item={item} service={service} />
      <span className={styles.actions}>
        {!embedded && item.state === 'ready' && <Button variant="primary" className={styles.rowButton} size="small" icon="i-download" disabled={!online} loading={saving.includes(item.id)} onClick={() => saveFile(item)}>{t('web.downloads.save')}</Button>}
        {item.actions.filter(action => action !== 'remove').map(action => <Button key={action} className={styles.rowButton} size="small" disabled={!online} loading={snapshot.busy.includes(item.id)} onClick={() => { if (action === 'cancel') setConfirmation({item, action}); else act(item, action); }}>{t(action === 'pause' ? 'action.pause' : action === 'resume' ? 'web.downloads.resume' : action === 'cancel' ? 'web.downloads.stop' : 'action.tryAgain')}</Button>)}
        <DownloadMenu items={[{id: 'go', label: t('web.downloads.goToTitle'), onSelect: () => { if (operations.current) effects.openItem(item.itemId); }},
          ...(item.actions.includes('remove') ? [{id: 'remove', label: t('action.removeShort'), destructive: true, disabled: snapshot.busy.includes(item.id), onSelect: () => setConfirmation({item, action: 'remove'})}] : [])]} />
      </span>
    </div>)}</div>
    {!!snapshot.requests.length && <><div className={styles.downloadHeading}><h2>{t('web.downloads.requestsTitle')}</h2></div><div className={styles.downloadList} role="list">{snapshot.requests.map(({request, title}) => <div className={styles.request} key={request.requestId} role="listitem"><b>{title}</b><span>{request.totalKnown ? t('web.downloads.requestProgress', {ready: request.ready, total: request.total}) : t('web.downloads.requestPreparing')}{request.failed > 0 && <small>{t('web.downloads.requestFailed', {failed: request.failed})}</small>}<Progress percent={request.totalKnown && request.total > 0 ? request.ready / request.total * 100 : undefined} /></span></div>)}</div></>}
    {snapshot.nextCursor && <Button size="small" className={styles.showMore} loading={snapshot.loadingMore} disabled={!online} onClick={() => { void service.loadMore(); }}>{t('action.showMore')}</Button>}
    <Dialog open={!!confirmation} onOpenChange={open => { if (!open && (!confirmation || !service.getSnapshot().busy.includes(confirmation.item.id))) setConfirmation(undefined); }} initialFocusRef={cancel}
      title={t(confirmation?.action === 'cancel' ? 'confirm.stopPreparing.title' : 'confirm.removePrepared.title')}
      description={t(confirmation?.action === 'cancel' ? 'confirm.stopPreparing.body' : 'confirm.removePrepared.body')}
      footer={<><Button ref={cancel} size="small" disabled={!!confirmation && snapshot.busy.includes(confirmation.item.id)} onClick={() => setConfirmation(undefined)}>{t('action.cancel')}</Button><Button size="small" variant="primary" disabled={!online} className={confirmation?.action === 'remove' ? styles.destructive : undefined} loading={!!confirmation && snapshot.busy.includes(confirmation.item.id)} onClick={() => { if (confirmation) act(confirmation.item, confirmation.action); }}>{t(confirmation?.action === 'cancel' ? 'web.downloads.stop' : 'action.removeShort')}</Button></>}><Fragment /></Dialog>
  </div>;
}
function ServerStatus({item, service}: {item: DownloadPreparation; service: DownloadsService}) {
  const percent = preparationPercent(item), label = item.state === 'running' ? percent === undefined ? t('web.downloads.state.preparing') : s('next.saved.preparing', {percent: Math.floor(percent)})
    : item.state === 'ready' ? t('web.downloads.state.ready') : item.state === 'queued' ? t('web.downloads.state.waiting') : item.state === 'paused' ? t('downloads.paused') : item.state === 'expired' ? t('web.downloads.state.expired') : service.reason(item);
  return <span className={styles.status}><b data-tone={item.state === 'ready' ? 'healthy' : undefined}>{label}</b>
    {item.state === 'ready' && item.expiresAt && <small>{t('web.downloads.keptUntil', {date: defaultI18n.date(item.expiresAt, 'dayMonth')})}</small>}
    {item.state === 'running' && item.progress.etaSeconds !== null && <small>{t('web.downloads.minutesLeft', {count: Math.max(1, Math.ceil(item.progress.etaSeconds / 60))})}</small>}
    {item.state === 'queued' && <small>{s('next.saved.waitingPreparation')}</small>}
    {item.state === 'paused' && percent !== undefined && <small>{s('next.saved.atPercent', {percent: Math.floor(percent)})}</small>}
    {item.state === 'expired' && <small>{s('next.saved.expiredHelp')}</small>}
    {item.state === 'running' && <Progress percent={percent} />}
  </span>;
}
