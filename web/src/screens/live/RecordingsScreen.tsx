import {useScreenIdentity} from '../detail/context';
import {serverNow} from '@core/server-clock.ts';
import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {bytesLabel, dvrMessage, playableRecording, recordingReason, recordingStateLabel,
  type Recording, type RecordingDetail, type RecordingState} from '@core/dvr.ts';
import {channelTint} from '@portico/client-core/kit-model';
import {Button, Chip, Segmented, Select} from '../../kit/controls';
import {ChannelMark} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Banner, Dialog, Menu, SidePanel, type MenuEntry} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {RecordingsLayout} from '../../kit/dvr/RecordingsLayout';
import {RuleGap} from '../../kit/dvr/RuleCard';
import {LiveRecordingTabs, RecordingPhoneTabs} from './SeriesRules';
import type {RecordingsController, RecordingsSort, RecordingsView} from './recordings-controller';
import {readRecordingDetail} from './recording-detail';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import type {ScreenContext} from '../detail/context';
import {useRecordingsLease} from './useRecordingsLease';
import {Tuners} from '../settings/server-livetv/recording/Tuners';
import type {TunerController} from '../settings/server-livetv/recording/tuner-controller';
import {useRecordedGroupLease, type RecordedGroupLease} from './useRecordedGroupLease';
import {useCommittedOpening, type Opening} from './useCommittedOpening';
import {router} from '../../router';
import styles from './live.module.css';

const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
const statePill = (state: RecordingState) => t(({scheduled: 'dvr.state.scheduled', conflicted: 'dvr.state.conflicted', 'waiting-source': 'dvr.state.waitingSource',
  'waiting-guide': 'dvr.state.waitingGuide', preparing: 'dvr.state.preparing', recording: 'dvr.state.recording', finalizing: 'dvr.state.finalizing',
  completed: 'dvr.state.completed', 'incomplete-playable': 'dvr.state.incompletePlayable', failed: 'dvr.state.failed', cancelled: 'dvr.state.cancelled', skipped: 'next.live.skipped',
  'pending-delete': 'dvr.state.pendingDelete', deleted: 'dvr.state.deleted'} as const)[state]);

function timeOf(value: string) { return new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {hour: 'numeric', minute: '2-digit', timeZone: defaultI18n.region.timeZone ?? undefined}).format(Date.parse(value)); }
function dayOf(value: string, now: number) {
  const at = Date.parse(value);
  return new Date(at).toDateString() === new Date(now).toDateString() ? t('guide.today')
    : new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {weekday: 'long', month: 'short', day: 'numeric'}).format(at);
}
function dateOf(value: string) { return new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {weekday: 'long', month: 'long', day: 'numeric'}).format(Date.parse(value)); }
function timeRange(start: string, end: string) {
  const from = timeOf(start), to = timeOf(end);
  const meridiem = from.match(/ (AM|PM)$/i)?.[1];
  const same = meridiem !== undefined && meridiem === to.match(/ (AM|PM)$/i)?.[1];
  return t('guide.cellTime', {start: same ? from.slice(0, -(meridiem.length + 1)) : from, end: to});
}
function rangeOf(recording: Recording) { return timeRange(recording.start, recording.end); }
/** Whether this viewer may change any profile's recording: the owner and administrators may; anyone else only their own.
 * The server sends no per-row "can change" fact, so a member is offered no changes in Everyone's (their own are in Mine). */
function changesEveryones(context: ScreenContext) { return context.identity?.viewer.role === 'owner' || context.identity?.viewer.role === 'admin'; }
/** A refused command, said on the row it was for. */
function RowRefusal({text}: {text?: string}) { return text ? <p className={styles.rowRefusal} role="alert">{text}</p> : null; }
/** What Delete removes: its size only when the server has said it. */
function deleteBody(recording: Recording | undefined) {
  return recording && recording.bytes > 0 ? t('web.dvr.deleteBody', {size: bytesLabel(recording.bytes)}) : t('next.live.deleteBody');
}
function episodeOf(recording: Recording) {
  const info = recording.programme.episode;
  const number = info?.number ?? (info as {number?: number; episode?: number} | undefined)?.episode;
  return info?.display ?? (info?.season !== undefined && number !== undefined ? t('downloads.title.episode', {season: info.season, episode: number}) : undefined);
}
function channelMark(recording: Recording) {
  const channel = recording.channel;
  return channel ? {sourceId: '', id: channel.id, text: channel.mark?.text ?? channel.name.slice(0, 2).toLocaleUpperCase(), seed: channel.mark?.seed ?? channel.id} : undefined;
}
function RecordingArt({recording, large, badges, now}: {recording: Recording; large?: boolean; badges?: ReactNode; now?: number}) {
  const mark = channelMark(recording);
  const title = recording.programme.title.replace(/^The\s+/i, '');
  const start = Date.parse(recording.start), end = Date.parse(recording.end);
  const progress = !large && recording.state === 'recording' && now !== undefined && end > start ? Math.min(1, Math.max(0, (now - start) / (end - start))) : undefined;
  return <span className={large ? styles.recArtLarge : styles.recArt} style={{'--tint': channelTint(mark?.seed ?? recording.id)} as CSSProperties} aria-hidden="true">
    <span className={styles.recArtGlyph}>{(Array.from(title)[0] ?? '?').toLocaleUpperCase()}</span>
    {mark && <span className={styles.recArtChannel}><ChannelMark mark={mark} size={large ? 22 : 18}/>{large && <b>{recording.channel?.name}</b>}</span>}
    {badges && <span className={styles.recArtBadges}>{badges}</span>}
    {progress !== undefined && <span className={styles.recArtProgress}><i style={{width: `${(progress * 100).toFixed(2)}%`}}/></span>}
  </span>;
}
function StatePill({recording}: {recording: Recording}) {
  return <span className={styles.recState} data-state={recording.state}>{statePill(recording.state)}</span>;
}
function RecordingCopy({recording}: {recording: Recording}) {
  const episode = episodeOf(recording), mark = channelMark(recording);
  return <div className={styles.recCopy}>
    <b>{recording.programme.title}</b>
    {(episode || recording.programme.subtitle) && <small className={styles.recEpisode}>{episode}{episode && recording.programme.subtitle && <RuleGap/>}{recording.programme.subtitle}</small>}
    <small className={styles.recMeta}>{recording.programme.flags?.live && <span className={styles.recFlag} data-kind="live">{t('guide.live')}</span>}{recording.programme.flags?.new && <span className={styles.recFlag} data-kind="new">{t('guide.new')}</span>}{recording.channel && <span>{mark && <ChannelMark mark={mark} size={18}/>}{recording.channel.name}</span>}{recording.ruleId ? <span><Icon name="i-series" size={15}/>{t('next.live.seriesRule')}</span> : <span>{t('next.live.oneTime')}</span>}{recording.bookedBy && <span>{t('next.live.bookedBy', {name: recording.bookedBy})}</span>}</small>
  </div>;
}
function RecordingRow({recording, now, onCancel, onDetails, refusal}: {recording: Recording; now: number; onCancel?: () => void; onDetails?: () => void; refusal?: string}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const items: readonly MenuEntry[] = [
    ...(onCancel ? [{id: 'cancel', label: t('recordings.cancelAction'), icon: 'i-close' as const, destructive: true, onSelect: onCancel}] : []),
    ...(onDetails ? [{id: 'details', label: t('next.live.details'), icon: 'i-info' as const, onSelect: onDetails}] : [])];
  return <div className={styles.recRow} data-state={recording.state}>
    <div className={styles.recWhen}><b>{recording.state === 'recording' ? t('guide.now') : timeOf(recording.start)}</b><small>{rangeOf(recording)}</small></div>
    <RecordingArt recording={recording} now={now}/>
    <RecordingCopy recording={recording}/>
    <div className={styles.recRight}>{recording.state !== 'scheduled' && <StatePill recording={recording}/>}
      {items.length > 0 && <Menu open={menuOpen} onOpenChange={open => setMenuOpen(open)} title={t('web.dvr.actionsFor', {title: recording.programme.title})}
        trigger={<Button className={styles.recMore} variant="ghost" size="small" iconOnly icon="i-more" aria-label={t('web.dvr.actionsFor', {title: recording.programme.title})}/>} items={items}/>}
    </div>
    <RowRefusal text={refusal}/>
  </div>;
}

function ConflictBox({context, recording, detail: supplied, onResolve, onCancel, onDetails, refusal}: {context: ScreenContext; recording: Recording; detail?: RecordingDetail; onResolve?: (detail?: RecordingDetail) => void; onCancel?: () => void; onDetails?: () => void; refusal?: string}) {
  const [detail, setDetail] = useState<RecordingDetail | undefined>(supplied);
  useEffect(() => {
    if (supplied || !context.isCurrent()) return;
    const abort = new AbortController();
    void readRecordingDetail(context, recording.id, abort.signal).then(value => {if (!abort.signal.aborted && context.isCurrent()) setDetail(value);}, () => {});
    return () => abort.abort();
  }, [context, recording.id, supplied]);
  const [menuOpen, setMenuOpen] = useState(false);
  const items: readonly MenuEntry[] = [
    ...(onCancel ? [{id: 'cancel', label: t('recordings.cancelAction'), icon: 'i-close' as const, destructive: true, onSelect: onCancel}] : []),
    ...(onDetails ? [{id: 'details', label: t('next.live.details'), icon: 'i-info' as const, onSelect: onDetails}] : [])];
  return <div className={styles.conflictBox}>
    <div className={styles.conflictHead}><Icon name="i-warn" size={22}/><div>
      <h3>{t('recordings.conflictTitle', {title: recording.programme.title})}</h3>
      {/* The server sends no tuner count to a viewer: the box says only what it knows. */}
      <p>{t('recordings.conflictsBody')}</p>
    </div></div>
    <div className={styles.conflictRow}>
      <div className={styles.recWhen}><b>{timeOf(recording.start)}</b><small>{rangeOf(recording)}</small></div>
      <RecordingArt recording={recording}/>
      <RecordingCopy recording={recording}/>
      <div className={styles.recRight}><StatePill recording={recording}/>{onResolve && <Button className={styles.conflictAction} variant="primary" onClick={() => onResolve(detail)}>{t('web.dvr.seeConflict')}</Button>}
        {items.length > 0 && <Menu open={menuOpen} onOpenChange={open => setMenuOpen(open)} title={t('web.dvr.actionsFor', {title: recording.programme.title})}
          trigger={<Button className={styles.recMore} variant="ghost" size="small" iconOnly icon="i-more" aria-label={t('web.dvr.actionsFor', {title: recording.programme.title})}/>} items={items}/>}</div>
    </div>
    <RowRefusal text={refusal}/>
    {detail && detail.overlaps.length > 0 && <div className={styles.conflictOthers}>
      <span>{t('next.live.usingTuners')}</span>
      {detail.overlaps.map(other => <span key={other.id}><b>{other.title}</b><em>{t('guide.cellTime', {start: timeOf(other.start), end: timeOf(other.end)})}</em></span>)}
    </div>}
  </div>;
}

type ConflictRequest = {lease: string; recording: Recording; detail?: RecordingDetail};
type ConflictOperation = {id: string; opening: Opening<ConflictRequest>; live: boolean};

export function conflictOperationCurrent(held: ConflictOperation, operationRef: {current: ConflictOperation | undefined}, opening: Opening<ConflictRequest>, current: () => boolean, requireOpening = false): boolean {
  const exact = () => operationRef.current === held && held.live && held.opening === opening && (!requireOpening || opening.live);
  return exact() && current() && exact();
}

function ConflictDialog({context, opening, current, consume, controller, onClose}: {
  context: ScreenContext; opening: Opening<ConflictRequest>; current: () => boolean; consume: (opening: Opening<ConflictRequest>) => boolean;
  controller: RecordingsController; onClose: (opening: Opening<ConflictRequest>) => void;
}) {
  const recording = opening.target.recording;
  const [detail, setDetail] = useState<RecordingDetail | undefined>(opening.target.detail);
  const [busy, setBusy] = useState<'raise' | 'skip'>();
  const [error, setError] = useState<string>();
  const [publishedOperation, publishOperation] = useState<ConflictOperation>();
  const operationRef = useRef<ConflictOperation | undefined>(undefined);
  useLayoutEffect(() => {
    // Strict effect replay creates another operation; it never revives one
    // whose layout cleanup already retired it.
    const held: ConflictOperation = {id: crypto.randomUUID(), opening, live: true};
    operationRef.current = held;
    publishOperation(held);
    return () => {held.live = false;if (operationRef.current === held) operationRef.current = undefined;};
  }, [opening]);
  const operation = publishedOperation?.live && publishedOperation.opening === opening && operationRef.current === publishedOperation ? publishedOperation : undefined;
  const owns = useCallback((held: ConflictOperation, requireOpening = false) => conflictOperationCurrent(held, operationRef, opening, current, requireOpening), [opening, current]);
  useEffect(() => {
    setError(undefined);
    if (!operation || opening.target.detail || !owns(operation, true)) return;
    const abort = new AbortController();
    void readRecordingDetail(context, recording.id, abort.signal).then(value => {if (!abort.signal.aborted && opening.live && owns(operation, true)) setDetail(value);},
      error => {if (!abort.signal.aborted && opening.live && owns(operation, true)) setError(dvrMessage(error));});
    return () => abort.abort();
  }, [context, opening, current, recording.id, operation, owns]);
  const run = async (kind: 'raise' | 'skip') => {
    const held = operation;
    if (busy || !held || !owns(held, true) || !consume(opening)) return;
    setBusy(kind);setError(undefined);
    try {
      const result = await controller.choose(recording, kind === 'skip' ? 'skip' : 'record');
      if (!owns(held)) return;
      // The server's answer: an airing it still cannot fit says so, and the dialog stays.
      if (result.committed && result.recording?.state === 'conflicted') {setError(t('next.live.stillConflicts'));return;}
      if (result.committed) {onClose(opening);return;}
      if (!result.busy) setError(dvrMessage({status: 409}));
    } catch (failure) {
      if (owns(held)) setError(dvrMessage(failure));
    } finally {if (owns(held)) setBusy(undefined);}
  };
  return <Dialog open onOpenChange={open => {if (!open) onClose(opening);}} width={480}
    title={t('recordings.conflictTitle', {title: recording.programme.title})} description={t('recordings.conflictsBody')}
    footer={<><Button variant="primary" loading={busy === 'raise'} disabled={!!busy || !opening.live} onClick={() => {void run('raise');}}>{t('web.dvr.recordThis')}</Button>
      <Button variant="glass" loading={busy === 'skip'} disabled={!!busy || !opening.live} onClick={() => {void run('skip');}}>{t('web.dvr.skipThis')}</Button></>}>
    {error ? <Banner>{error}</Banner> : <p className={styles.dialogText}>{t('recordings.conflictsBody')}</p>}
  </Dialog>;
}

export function RecordingsPager({controller, snapshot}: {controller: RecordingsController; snapshot: ReturnType<RecordingsController['getSnapshot']>}) {
  const paging = snapshot.paging;
  if (!paging) return null;
  const move = (direction: 'more' | 'previous' | 'retryPage') => {void controller[direction](snapshot).catch(() => {});};
  return <div className={styles.groupMore}>
    {paging.error && <Banner action={{label: t('next.states.tryAgain'), onAction: () => move('retryPage')}}>{paging.error}</Banner>}
    {paging.previous && <Button variant="glass" disabled={paging.busy || snapshot.mutating} onClick={() => move('previous')}>{t('web.pager.previous')}</Button>}
    {paging.next && <Button variant="glass" loading={paging.busy} disabled={snapshot.mutating} onClick={() => move('more')}>{t('next.live.more')}</Button>}
  </div>;
}

export function UpcomingView({context, controller, snapshot, now, onResolve, conflictDetails, canChange = true}: {context: ScreenContext; controller: RecordingsController; snapshot: ReturnType<RecordingsController['getSnapshot']>; now: number; onResolve: (recording: Recording, detail?: RecordingDetail) => void; conflictDetails?: Readonly<Record<string, RecordingDetail>>; canChange?: boolean}) {
  const refusal = (recording: Recording) => snapshot.commandRecordingId === recording.id ? snapshot.commandError : undefined;
  const cancel = (recording: Recording) => canChange ? () => {void controller.cancel(recording);} : undefined;
  const recordings = snapshot.page?.recordings ?? [];
  const conflicted = recordings.filter(recording => recording.state === 'conflicted');
  const current = recordings.filter(recording => ['recording', 'preparing', 'finalizing'].includes(recording.state) && recording.state !== 'conflicted');
  const later = recordings.filter(recording => ['scheduled', 'waiting-source', 'waiting-guide'].includes(recording.state));
  const [shown, setShown] = useState<Recording>();
  const closeShown = (target: Recording | undefined) => setShown(current => current === target ? undefined : current);
  return <div className={styles.recBody}>
    {conflicted.length > 0 && <section className={styles.recSection}><h2>{t('recordings.conflicts')}<span>{snapshot.summary?.conflicts ?? conflicted.length}</span></h2>
      {conflicted.map(recording => <ConflictBox key={recording.id} context={context} recording={recording} detail={conflictDetails?.[recording.id]} {...(canChange ? {onResolve: (detail?: RecordingDetail) => onResolve(recording, detail)} : {})} onCancel={cancel(recording)} onDetails={() => setShown(recording)} refusal={refusal(recording)}/>)}</section>}
    {current.length > 0 && <section className={styles.recSection}><h2>{t('next.live.recordingNow')}</h2>
      {current.map(recording => <RecordingRow key={recording.id} recording={recording} now={now} onCancel={cancel(recording)} refusal={refusal(recording)}/>)}</section>}
    {later.length > 0 && <section className={styles.recSection}><h2>{t('next.live.laterToday')}<span>{dateOf(later[0]!.start)}</span></h2>
      {later.map(recording => <RecordingRow key={recording.id} recording={recording} now={now} onCancel={cancel(recording)} onDetails={() => setShown(recording)} refusal={refusal(recording)}/>)}</section>}
    {!conflicted.length && !current.length && !later.length && <StateView title={t('recordings.noneScheduled')} body={t('recordings.noneScheduledBody')}/>}
    <RecordingsPager controller={controller} snapshot={snapshot}/>
    <Dialog open={!!shown} onOpenChange={open => {if (!open) closeShown(shown);}} title={shown?.programme.title} description={shown ? rangeOf(shown) : undefined} width={440}
      footer={<Button variant="glass" onClick={() => closeShown(shown)}>{t('next.overlays.close')}</Button>}>{shown && <p className={`${styles.dialogText} ${styles.dialogFacts}`}><span>{recordingStateLabel(shown.state)}</span>{shown.reason && <span>{recordingReason(shown.reason)}</span>}</p>}</Dialog>
  </div>;
}

export function RecordedView({context, controller, current, snapshot, everyone = false}: {context: ScreenContext; controller: RecordingsController; current: () => boolean; snapshot: ReturnType<RecordingsController['getSnapshot']>; everyone?: boolean}) {
  const [open, setOpen] = useState<{key: string; title: string}>();
  const sort = (snapshot.groupsSort ?? 'recent') as RecordingsSort;
  const groups = snapshot.groups?.groups ?? [];
  const change = (value: string) => {if (current() && (value === 'recent' || value === 'title' || value === 'oldest')) void controller.readGroups(value).catch(() => {});};
  return <div className={styles.recBody}>
    <div className={styles.recordedHead}><p>{t('next.live.recordedHead', {shows: snapshot.summary?.groups ?? groups.length, recordings: snapshot.summary?.recorded ?? 0, unwatched: snapshot.summary?.unwatched ?? 0})}</p>
      <span className={styles.recordedSortLabel}>{t('channels.sort')}</span><Select className={styles.recordedSort} aria-label={t('channels.sort')} value={sort} options={[{value: 'recent', label: t('next.live.sortRecent')}, {value: 'title', label: t('next.live.sortTitle')}, {value: 'oldest', label: t('next.live.sortOldest')}]} onValueChange={change}/></div>
    {groups.length > 0 ? <div className={styles.recordedGrid}>{groups.map(group => <button type="button" className={styles.recordedCard} key={group.key} onClick={() => {if (current()) setOpen({key: group.key, title: group.title});}}>
      <RecordingArt recording={group.recording} large={true} badges={<>{group.unwatched > 0 && <em className={styles.recArtCount}>{group.unwatched}</em>}{group.kept > 0 && <em>{t('recordings.kept')}</em>}{group.exceptions > 0 && <em>{t('dvr.state.incomplete')}</em>}</>}/>
      <span className={styles.recordedCopy}><b>{group.title}</b><small>{group.recording.channel?.name}</small>
        <small>{group.unwatched ? t('recordings.showCountUnwatched', {count: group.recordings, unwatched: group.unwatched}) : t('recordings.showCount', {count: group.recordings})}</small></span>
    </button>)}</div> : <StateView title={t('recordings.noneRecorded')} body={t('recordings.noneRecordedBody')}/>}
    <RecordingsPager controller={controller} snapshot={snapshot}/>
    {open && <RecordedGroupPanel key={open.key} context={context} current={current} group={open.key} title={open.title} sort={sort} everyone={everyone} onClose={() => setOpen(value => value === open ? undefined : value)}/>}
  </div>;
}

function RecordedGroupPanel({context, current, group, title, sort, everyone, onClose}: {context: ScreenContext; current: () => boolean; group: string; title: string; sort: RecordingsSort; everyone: boolean; onClose: () => void}) {
  const lease = useRecordedGroupLease(context, group, sort, current, everyone);
  return lease ? <CommittedRecordedGroupPanel key={lease.id} lease={lease} title={title} onClose={onClose}/> : null;
}
function CommittedRecordedGroupPanel({lease, title, onClose}: {lease: RecordedGroupLease; title: string; onClose: () => void}) {
  const state = useSyncExternalStore(lease.reader.subscribe, lease.reader.getSnapshot, lease.reader.getSnapshot);
  const canChange = !lease.everyone || changesEveryones(lease.context);
  type DeleteRequest = {token: string; recording: Recording};
  const [removeRequest, setRemoveRequest] = useState<DeleteRequest>();
  const {opening: removeOpening, consume: consumeRemove} = useCommittedOpening(removeRequest);
  // Retire this exact panel and its play lifetime before the parent closes it,
  // so a retained same-tick command cannot act in the layout-cleanup gap.
  const closePanel = () => {
    if (!lease.live) return;
    lease.live = false;lease.playLife.dispose();onClose();
  };
  const resident = (recording: Recording) => lease.reader.getSnapshot().recordings.includes(recording);
  const requestRemove = (recording: Recording) => {
    if (!lease.current() || !resident(recording)) return;
    setRemoveRequest({token: crypto.randomUUID(), recording});
  };
  const dismissRemove = (opening: Opening<DeleteRequest>) => {
    if (!consumeRemove(opening)) return;
    setRemoveRequest(current => current === opening.target ? undefined : current);
  };
  const confirmRemove = (opening: Opening<DeleteRequest>) => {
    if (!lease.current() || !consumeRemove(opening)) return;
    const recording = opening.target.recording;
    setRemoveRequest(current => current === opening.target ? undefined : current);
    if (!resident(recording)) return;
    void lease.reader.remove(recording);
  };
  const play = (recording: Recording) => {
    if (!lease.current() || !resident(recording) || !playableRecording(recording)) return;
    void lease.context.effects.play({libraryId: recording.libraryId, itemId: recording.itemId, startSeconds: Math.max(0, (recording.positionMs ?? 0) / 1000)},
      lease.playLife.signal(), () => lease.current() && resident(recording)).catch(error => {if (lease.current()) lease.context.effects.reportError(error);});
  };
  const keep = (recording: Recording) => {
    if (lease.current() && resident(recording)) void lease.reader.keep(recording);
  };
  return <SidePanel open onOpenChange={open => {if (!open) closePanel();}} width={640} topInset={76} title={title}
    description={t('recordings.showCount', {count: state.recordings.length})} className={styles.groupPanel}
    footer={<Button variant="glass" onClick={closePanel}>{t('next.overlays.close')}</Button>}>
    {state.error && !state.errorRecordingId && <Banner>{state.error}</Banner>}
    {state.recordings.map(recording => <div className={styles.groupRow} key={recording.id}>
      <RecordingArt recording={recording}/>
      <div className={styles.groupCopy}><b>{recording.programme.subtitle || recording.programme.title}</b><small>{dayOf(recording.start, serverNow())}<RuleGap/>{rangeOf(recording)}</small>
        <small>{recording.keep ? t('recordings.kept') : recording.watched === true ? t('next.live.watched') : recording.watched === false ? t('next.live.unwatched') : null}</small></div>
      <div className={styles.groupActions}>
        <Button variant="glass" size="small" icon="i-play" disabled={!playableRecording(recording)} onClick={() => play(recording)}>{t('next.live.play')}</Button>
        {canChange && <Button variant="ghost" size="small" disabled={state.busy || !playableRecording(recording)} onClick={() => keep(recording)}>{recording.keep ? t('web.dvr.stopKeeping') : t('web.dvr.keep')}</Button>}
        {canChange && <Button variant="ghost" size="small" disabled={state.busy} onClick={() => requestRemove(recording)}>{t('web.dvr.deleteRecording')}</Button>}
      </div>
      <RowRefusal text={state.errorRecordingId === recording.id ? state.error : undefined}/>
    </div>)}
    {state.nextCursor && <Button className={styles.groupMore} variant="glass" onClick={() => {if (lease.current()) void lease.reader.more().catch(() => {});}}>{t('next.live.more')}</Button>}
    <Dialog open={!!removeOpening} onOpenChange={open => {if (!open && removeOpening) dismissRemove(removeOpening);}} width={440} title={t('web.dvr.deleteTitle', {title: removeOpening?.target.recording.programme.title ?? ''})}
      description={deleteBody(removeOpening?.target.recording)} footer={<><Button variant="glass" onClick={() => {if (removeOpening) dismissRemove(removeOpening);}}>{t('action.cancel')}</Button>
        <Button variant="ghost" loading={state.busy} onClick={() => {if (removeOpening) confirmRemove(removeOpening);}}>{t('web.dvr.deleteRecording')}</Button></>}>
      <p className={styles.dialogText}>{deleteBody(removeOpening?.target.recording)}</p>
    </Dialog>
  </SidePanel>;
}

export function HistoryView({snapshot, now, controller}: {controller: RecordingsController; snapshot: ReturnType<RecordingsController['getSnapshot']>; now: number}) {
  const recordings = snapshot.page?.recordings ?? [], [shown, setShown] = useState<Recording>();
  const closeShown = (target: Recording | undefined) => setShown(current => current === target ? undefined : current);
  return <div className={styles.recBody}>{recordings.length
    ? recordings.map(recording => <RecordingRow key={recording.id} recording={recording} now={now} onDetails={() => setShown(recording)}/>)
    : <StateView title={t('recordings.noneRecorded')} body={t('recordings.noneRecordedBody')}/>}
    <RecordingsPager controller={controller} snapshot={snapshot}/>
    <Dialog open={!!shown} onOpenChange={open => {if (!open) closeShown(shown);}} title={shown?.programme.title} description={shown ? rangeOf(shown) : undefined} width={440}
      footer={<Button variant="glass" onClick={() => closeShown(shown)}>{t('next.overlays.close')}</Button>}>{shown && <p className={`${styles.dialogText} ${styles.dialogFacts}`}><span>{recordingStateLabel(shown.state)}</span>{shown.reason && <span>{recordingReason(shown.reason)}</span>}</p>}</Dialog>
  </div>;
}

function StorageView({snapshot, tuners}: {snapshot: ReturnType<RecordingsController['getSnapshot']>; tuners?: TunerController}) {
  const storage = snapshot.storage;
  if (!storage) return null;
  // What the server says about the recordings' space: free, about how many hours are left, and whether the folder can be written.
  return <div className={styles.recBody}><div className={styles.storagePanel}>
    <p><b>{t('next.live.storageFree', {amount: Math.round(storage.freeBytes / 1e9)})}</b> · {t('recordings.hoursLeft', {count: storage.hoursLeft})}</p>
    {!storage.writeHealthy && <Banner>{t('recordings.unavailableBody')}</Banner>}
  </div>{tuners && <Tuners controller={tuners} detailed/>}</div>;
}

function Boundary({context, view, everyone, routeCurrent, onView, onScope, onGuide}: {context: ScreenContext; view: RecordingsView; everyone: boolean; routeCurrent: () => boolean;
  onView: (view: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => void; onScope?: (everyone: boolean) => void; onGuide: () => void}) {
  const [attempt, setAttempt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useScreenIdentity(context);
  const lease = useRecordingsLease(context, view, attempt, routeCurrent, everyone);
  const snapshot = useSyncExternalStore(lease?.controller.subscribe ?? noSubscribe, lease?.controller.getSnapshot ?? noSnapshot, lease?.controller.getSnapshot ?? noSnapshot);
  const summary = snapshot?.summary;
  const views = [
    {id: 'upcoming' as const, label: t('recordings.upcoming'), count: summary?.upcoming},
    {id: 'recorded' as const, label: t('recordings.recorded'), count: summary?.recorded},
    {id: 'rules' as const, label: t('recordings.rules'), count: summary?.rules},
    {id: 'history' as const, label: t('saved.tab.history')},
    {id: 'storage' as const, label: t('recordings.storage')},
  ];
  const [conflictRequest, setConflictRequest] = useState<ConflictRequest>();
  const {opening: conflictOpening, consume: consumeConflict} = useCommittedOpening(conflictRequest);
  useLayoutEffect(() => {setConflictRequest(undefined);}, [lease?.id]);
  const retry = () => {if (!lease?.current()) return;setNow(Date.now());setAttempt(value => value + 1);};
  const goView = (next: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => {if (lease?.current()) onView(next);};
  const goGuide = () => {if (lease?.current()) onGuide();};
  const openConflict = (recording: Recording, detail?: RecordingDetail) => {
    if (!lease?.current()) return;
    setConflictRequest({lease: lease.id, recording, detail});
  };
  const dismissConflict = (opening: Opening<ConflictRequest>) => {
    consumeConflict(opening);
    setConflictRequest(current => current === opening.target ? undefined : current);
  };
  if (!lease || !snapshot) return null;
  const loading = snapshot.phase === 'loading' || snapshot.phase === 'idle';
  const body = loading ? <p role="status">{t('recordings.loading')}</p>
    : snapshot.error && !snapshot.page && !snapshot.storage ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: retry}}/>
      : view === 'upcoming' ? <UpcomingView context={context} controller={lease.controller} snapshot={snapshot} now={now} onResolve={openConflict} canChange={!everyone || changesEveryones(context)}/>
        : view === 'recorded' ? <RecordedView context={context} controller={lease.controller} current={lease.current} snapshot={snapshot} everyone={everyone}/>
          : view === 'history' ? <HistoryView snapshot={snapshot} now={now} controller={lease.controller}/>
            : <StorageView snapshot={snapshot} tuners={lease.tuners}/>;
  return <RecordingsLayout key={lease.id} title={t('next.home.shell.live')} sourceLabel={t('channels.source')} allChannels={t('channels.all')}
    sourceName={context.identity?.serverName ?? ''} navigation={undefined} back={{label: t('next.home.shell.live'), title: t('recordings.title'), onBack: goGuide}}>
    <LiveRecordingTabs view="recordings" conflicts={summary?.conflicts} onChange={value => {if (value === 'guide') goGuide();}}/>
    <RecordingPhoneTabs views={views} value={view} onChange={goView}/>
    <div className={styles.recordingsBar}>
      <div className={styles.recordingsChips} role="group" aria-label={t('recordings.title')}>
        {views.map(item => <Chip className={styles.recordingsChip} key={item.id} selected={item.id === view} count={item.count} onClick={() => goView(item.id)}>{item.label}</Chip>)}
      </div>
      {/* Recordings open on this profile's own, with everyone's one tap away (rule 48). */}
      {onScope && (view === 'upcoming' || view === 'recorded' || view === 'history') && <Segmented className={styles.recordingsScope} aria-label={t('next.live.whose')} value={everyone ? 'everyone' : 'mine'}
        onValueChange={value => {if (lease.current() && (value === 'mine' || value === 'everyone')) onScope(value === 'everyone');}}
        items={[{value: 'mine', label: t('next.live.mine')}, {value: 'everyone', label: t('next.live.everyones')}]}/>}
      {snapshot.storage && <button type="button" className={styles.storageButton} aria-label={t('recordings.storage')} onClick={() => goView('storage')}>
        <span>{t('next.live.storageFree', {amount: Math.round(snapshot.storage.freeBytes / 1e9)})} · {t('recordings.hoursLeft', {count: snapshot.storage.hoursLeft})}</span>
      </button>}
    </div>
    {snapshot.error && <Banner className={styles.recordingsBanner} action={{label: t('next.states.tryAgain'), onAction: retry}}>{snapshot.error}</Banner>}
    {body}
    {conflictOpening && <ConflictDialog key={conflictOpening.id} context={context} opening={conflictOpening} current={lease.current} consume={consumeConflict}
      controller={lease.controller} onClose={dismissConflict}/>}
  </RecordingsLayout>;
}

export function RecordingsScreen({captured, view, everyone = false, onView, onScope, onGuide}: {captured: ProtectedLibraryScreen; view: RecordingsView; everyone?: boolean; onView: (view: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => void; onScope?: (everyone: boolean) => void; onGuide: () => void}) {
  const pathname = useLocation({select: location => location.pathname});
  const context = useCommittedLibraryScreenContext(captured, pathname, () => router.state.location.pathname === pathname);
  const routeCurrent = () => {
    const search = router.state.location.search as unknown as Record<string, unknown>;
    return router.state.location.pathname === pathname && search.tab === 'recordings' && (search.dvr ?? 'upcoming') === view && (search.scope === 'everyone') === everyone;
  };
  return context ? <Boundary context={context} view={view} everyone={everyone} routeCurrent={routeCurrent} onView={onView} onScope={onScope} onGuide={onGuide}/> : null;
}
