import React, {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {BackHandler, Text, View} from 'react-native';
import {parseSegmentClock, segmentClock, segmentKinds, type SegmentKind, type SegmentReviewEntry, type SegmentReviewMarker} from '../../../../../../packages/client-core/src/segment-review';
import {parseMediaItem} from '../../../../../../packages/client-core/src/media-item';
import {validateContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {encodedRoutePath} from '../../../../../../packages/contracts/src/routes';
import {Artwork} from '../../../kit/artwork';
import {Button, Select, TextField} from '../../../kit/controls';
import {BottomSheet, Dialog} from '../../../kit/overlays';
import {SegmentWindows, type SegmentWindow} from '../../../kit/shell/SegmentWindows';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsPanelDeps} from '../settings-panels';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeSegmentReviewController} from './segment-review-controller';

/** The phone review borrows the selected owner's existing settings lifetime and API. */
export function IntroCreditsReview({context, lifetime, t, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const theme = useTheme(), alive = useRef(true);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const api = useMemo(() => ({request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)}), [context, lifetime]);
  const controller = useMemo(() => new NativeSegmentReviewController(api, lifetime), [api, lifetime]);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [seasonNumber, setSeasonNumber] = useState<number>(), [confirm, setConfirm] = useState(false), [adding, setAdding] = useState<SegmentReviewEntry>();
  const [kind, setKind] = useState<SegmentKind>('intro'), [start, setStart] = useState('0:00'), [end, setEnd] = useState('0:01');
  useEffect(() => {void controller.open(); return () => controller.dispose();}, [controller]);
  useEffect(() => {
    if (state.titleId || state.libraryId) return;
    const timer = setInterval(() => {const current = controller.getSnapshot(); if (!current.busy && !current.loading) void controller.reload();}, 15000);
    return () => clearInterval(timer);
  }, [controller, state.libraryId, state.titleId]);
  useEffect(() => {const listener = BackHandler.addEventListener('hardwareBackPress', () => {
    if (state.busy) return true;
    if (state.titleId) {void controller.open(state.libraryId); return true;}
    if (state.libraryId) {void controller.open(); return true;}
    return false;
  }); return () => listener.remove();}, [controller, state.busy, state.libraryId, state.titleId]);
  const run = (work: () => Promise<unknown>) => {void work().catch(error => {if (alive.current && lifetime.current()) runtime.reportError(error);});};
  const watch = async (entry: SegmentReviewEntry, _marker: SegmentReviewMarker, fromUS: string) => {
    const item = await lifetime.request(signal => context.api.request(encodedRoutePath('get_items_id', {id: entry.itemId}), 'GET', undefined, signal).then(parseMediaItem));
    lifetime.assert();
    if (!alive.current || controller.getSnapshot().review !== state.review || item.id !== entry.itemId || item.libraryId !== state.libraryId || !item.available) return;
    const display = validateContentEntry({id: item.id, libraryId: item.libraryId, kind: item.kind, title: item.title, available: item.available, year: item.year, posterUrl: item.posterUrl, backdropUrl: item.backdropUrl, overview: item.overview});
    const target = {itemId: item.id, startSeconds: Number(fromUS) / 1e6};
    await context.playback.start({entry: {...display, duration: item.duration, playback: target}, target, startIntent: 'explicit'});
  };
  const copy = (value: string, secondary = false) => <Text style={[secondary ? theme.type.caption : theme.type.bodyStrong, {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{value}</Text>;
  const card = {padding: 14, gap: 12, borderRadius: 14, backgroundColor: theme.colors.raised};
  const review = state.review, season = review?.seasons.find(value => value.number === seasonNumber) ?? review?.seasons[0];
  const startUS = parseSegmentClock(start), endUS = parseSegmentClock(end);
  const valid = !!adding && startUS !== null && endUS !== null && Number(startUS) < Number(endUS) && Number(endUS) <= Number(adding.durationUS);
  const windows = (entry: SegmentReviewEntry): readonly [SegmentWindow, SegmentWindow] => {
    const duration = Number(entry.durationUS), span = Math.min(duration, 600e6);
    return [{id: 'opening', label: t('next.shell.opening'), startUS: '0', endUS: String(span)}, {id: 'end', label: t('next.shell.end'), startUS: String(Math.max(0, duration - span)), endUS: String(duration)}];
  };
  return <View testID="phone-segment-review" style={{gap: 14, paddingVertical: 14}}>
    {(state.libraryId || state.titleId) && <Button variant="text" disabled={state.busy} onPress={() => {setSeasonNumber(undefined); void controller.open(state.titleId ? state.libraryId : undefined);}}>{t('action.back')}</Button>}
    {state.error ? <StateView variant="inline" {...runtime.error(state.error, () => {void controller.reload();})}/> : null}
    {state.loading && copy(t('web.segments.loading'), true)}
    {state.notice && <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.text}]}>{t(state.notice.kind === 'approved' ? 'web.segments.approvedCount' : 'web.segments.detectAgainQueued', {count: state.notice.count})}</Text>}
    {!state.libraryId && state.status && <>
      <View style={card}>{copy(t('web.segments.progress'))}<View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 20}}>
        {[[t('web.segments.measured'), state.status.files.complete], [t('web.segments.waiting'), state.status.files.pending], [t('web.segments.unreadable'), state.status.files.failed + state.status.files.unsupported]].map(([label, count]) => <View key={String(label)}>{copy(String(count))}{copy(String(label), true)}</View>)}
      </View>{copy(t(state.status.mode === 'off' ? 'web.segments.stateOff' : state.status.catchUpComplete ? 'web.segments.stateDone' : 'web.segments.stateWorking'), true)}</View>
      {copy(t('web.segments.libraries'))}
      {state.status.libraries.map(library => <View key={library.libraryId} style={card}>{copy(library.name)}{copy(t(library.itemKind === 'movie' ? 'web.segments.libraryModeFilms' : 'web.segments.libraryMode', {mode: library.mode}), true)}{library.choice === 'server' && copy(t('web.segments.followsServer'), true)}<Button variant="glass" onPress={() => {void controller.open(library.libraryId);}}>{t('web.segments.review')}</Button></View>)}
    </>}
    {state.page && <>
      {!state.page.titles.length && copy(t('web.segments.noTitles'), true)}
      {state.page.titles.map(title => <View key={title.id} style={card}><View style={{flexDirection: 'row', gap: 12, alignItems: 'center'}}><SegmentPoster id={title.id} libraryId={state.page!.libraryId} context={context} lifetime={lifetime} runtime={runtime}/><View style={{flex: 1}}>{copy(title.title)}</View></View>{copy(t('web.segments.titleFiles', {measured: title.files.complete, total: title.files.complete + title.files.pending + title.files.failed + title.files.unsupported}), true)}
        {title.markers.suggested > 0 && copy(t('web.segments.titleSuggested', {count: title.markers.suggested}))}{title.markers.approved > 0 && copy(t('web.segments.titleApproved', {count: title.markers.approved}), true)}{title.markers.automatic > 0 && copy(t('web.segments.titleAutomatic', {count: title.markers.automatic}), true)}
        <Button variant="glass" onPress={() => {setSeasonNumber(undefined); void controller.open(state.libraryId, title.id);}}>{t('web.segments.review')}</Button></View>)}
      {!!state.page.nextCursor && <Button variant="text" disabled={state.loading} onPress={() => {void controller.open(state.libraryId, undefined, true);}}>{t('web.segments.showMore')}</Button>}
    </>}
    {review && <>{copy(review.title)}{review.seasons.length > 1 && <Select accessibilityLabel={t('web.segments.episodes')} value={String(season?.number)} options={review.seasons.map(value => ({value: String(value.number), label: value.title || t(value.number === 0 ? 'web.segments.specials' : 'web.segments.season', {number: value.number})}))} onValueChange={value => setSeasonNumber(Number(value))}/>}
      <View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" disabled={state.busy} onPress={() => run(() => controller.approve(season?.number))}>{t('web.segments.approveSeason')}</Button><Button variant="glass" disabled={state.busy} onPress={() => setConfirm(true)}>{t('web.segments.detectAgain')}</Button></View>
      {season?.entries.map(entry => <View key={entry.itemId} style={card}>{copy(segmentClock(entry.durationUS), true)}{entry.markers.some(marker => !marker.approved && !marker.automaticSafe && marker.viewerVisible) && copy(t('web.segments.toReview', {count: entry.markers.filter(marker => !marker.approved && !marker.automaticSafe && marker.viewerVisible).length}))}
        <SegmentWindows entry={entry} windows={windows(entry)} saving={state.busy} onKindChange={(marker, nextKind) => controller.edit(entry, {action: 'edit', markerId: marker.id, kind: nextKind, startUS: marker.startUS, endUS: marker.endUS})} onSave={(marker, bounds) => controller.edit(entry, {action: 'edit', markerId: marker.id, kind: marker.kind, ...bounds})}
          onApprove={marker => run(() => controller.edit(entry, {action: 'approve', markerId: marker.id}))} onDismiss={marker => run(() => controller.edit(entry, {action: 'dismiss', markerId: marker.id}))}
          onWatch={(marker, fromUS) => run(() => watch(entry, marker, fromUS))} onAdd={() => {setAdding(entry); setKind('intro'); setStart('0:00'); setEnd(segmentClock(String(Math.min(1e6, Number(entry.durationUS)))));}}/>
        {!entry.markers.length && copy(t('web.segments.entryState', {state: entry.state}), true)}
      </View>)}
    </>}
    <Dialog open={confirm} onOpenChange={setConfirm} title={t('web.segments.detectAgain')} description={t('web.segments.detectAgainHelp')} footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" onPress={() => setConfirm(false)}>{t('action.cancel')}</Button><Button disabled={state.busy} onPress={() => {setConfirm(false); run(() => controller.redetect());}}>{t('web.segments.detectAgain')}</Button></View>}><View/></Dialog>
    <BottomSheet open={!!adding} onOpenChange={open => {if (!open) setAdding(undefined);}} title={t('web.segments.addTitle')} description={t('web.segments.timeHelp')} footer={<Button disabled={!valid || state.busy} onPress={() => {if (adding && valid) run(async () => {await controller.edit(adding, {action: 'create', kind, startUS: startUS!, endUS: endUS!}); setAdding(undefined);});}}>{t('action.save')}</Button>}>
      <View style={{gap: 14, padding: 20}}><Select label={t('web.segments.kindLabel')} value={kind} options={segmentKinds.map(value => ({value, label: t('web.segments.kind', {kind: value})}))} onValueChange={value => setKind(value as SegmentKind)}/><TextField label={t('web.segments.start')} value={start} onChangeText={setStart}/><TextField label={t('web.segments.end')} value={end} onChangeText={setEnd}/></View>
    </BottomSheet>
  </View>;
}

function SegmentPoster({id, libraryId, context, lifetime, runtime}: Pick<NativeSettingsPanelDeps, 'context' | 'lifetime'> & {id: string; libraryId: string; runtime: NativeShellRuntime}) {
  const [path, setPath] = useState<string>();
  useEffect(() => {
    const read = new AbortController();
    void lifetime.request(signal => context.api.request(encodedRoutePath('get_items_id', {id}), 'GET', undefined, signal).then(parseMediaItem), read.signal).then(item => {
      if (!read.signal.aborted && item.id === id && item.libraryId === libraryId) setPath(item.posterUrl);
    }, () => { /* Missing artwork retains the kit's honest empty surface. */ });
    return () => read.abort();
  }, [context, id, libraryId, lifetime]);
  return <Artwork source={runtime.artwork(path)} width={48} height={72}/>;
}
