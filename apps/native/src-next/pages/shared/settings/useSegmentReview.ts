import {readCatalogueItemEncodedResponse} from '../../../../../../packages/client-core/src/query/catalogue-reads';
import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {parseSegmentClock, segmentClock, type SegmentKind, type SegmentReviewEntry, type SegmentReviewMarker} from '../../../../../../packages/client-core/src/segment-review';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {parseMediaItem} from '../../../../../../packages/client-core/src/media-item';
import {validateContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SegmentWindow} from '../../../kit/shell/SegmentWindows';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeSegmentReviewController} from '../../../screens/settings-live/owner-libraries/segment-review-controller';
import {registerBack} from '../../../kit/navigation/back';

/** The phone review borrows the selected owner's existing settings lifetime and API. */
export function useSegmentReview({context, lifetime, t, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const alive = useRef(true);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const api = useMemo(() => ({request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)}), [context, lifetime]);
  const controller = useMemo(() => new NativeSegmentReviewController(api, lifetime), [api, lifetime]);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [seasonNumber, setSeasonNumber] = useState<number>(), [confirm, setConfirm] = useState(false), [adding, setAdding] = useState<SegmentReviewEntry>();
  const [kind, setKind] = useState<SegmentKind>('intro'), [start, setStart] = useState('0:00'), [end, setEnd] = useState('0:01');
  useEffect(() => {void controller.open(); return () => controller.dispose();}, [controller]);
  useEffect(() => {
    if (state.titleId || state.libraryId) return;
    // reload keeps its failure in the snapshot; rethrowing it lets repeat back the next read off.
    const poll = repeat(async () => {
      const current = controller.getSnapshot();
      if (current.busy || current.loading) return;
      await controller.reload();
      const {error} = controller.getSnapshot();
      if (error) throw error;
    }, {everyMs: 15_000, backoff: {baseMs: 30_000, maxMs: 120_000}, immediate: false});
    return () => poll.stop();
  }, [controller, state.libraryId, state.titleId]);
  useEffect(() => {const listener = registerBack(() => {
    if (state.busy) return true;
    if (state.titleId) {void controller.open(state.libraryId); return true;}
    if (state.libraryId) {void controller.open(); return true;}
    return false;
  }, {scope: 'surface'}); return () => listener();}, [controller, state.busy, state.libraryId, state.titleId]);
  const run = (work: () => Promise<unknown>) => {void work().catch(error => {if (alive.current && lifetime.current()) runtime.reportError(error);});};
  const watch = async (entry: SegmentReviewEntry, _marker: SegmentReviewMarker, fromUS: string) => {
    const item = await lifetime.request(signal => readCatalogueItemEncodedResponse(context.api, entry.itemId, signal).then(parseMediaItem));
    lifetime.assert();
    if (!alive.current || controller.getSnapshot().review !== state.review || item.id !== entry.itemId || item.libraryId !== state.libraryId || !item.available) return;
    const display = validateContentEntry({id: item.id, libraryId: item.libraryId, kind: item.kind, title: item.title, available: item.available, year: item.year, posterUrl: item.posterUrl, backdropUrl: item.backdropUrl, overview: item.overview});
    const target = {itemId: item.id, startSeconds: Number(fromUS) / 1e6};
    await context.playback.start({entry: {...display, duration: item.duration, playback: target}, target, startIntent: 'explicit'});
  };
  const review = state.review, season = review?.seasons.find(value => value.number === seasonNumber) ?? review?.seasons[0];
  const startUS = parseSegmentClock(start), endUS = parseSegmentClock(end);
  const valid = !!adding && startUS !== null && endUS !== null && Number(startUS) < Number(endUS) && Number(endUS) <= Number(adding.durationUS);
  const windows = (entry: SegmentReviewEntry): readonly [SegmentWindow, SegmentWindow] => {
    const duration = Number(entry.durationUS), span = Math.min(duration, 600e6);
    return [{id: 'opening', label: t('next.shell.opening'), startUS: '0', endUS: String(span)},
      {id: 'end', label: t('next.shell.end'), startUS: String(Math.max(0, duration - span)), endUS: String(duration)}];
  };
  const open = (libraryId?: string, titleId?: string) => {setSeasonNumber(undefined); void controller.open(libraryId, titleId);};
  return {
    state, review, season, selectSeason: (value: string) => setSeasonNumber(Number(value)), open, windows,
    goBack: () => open(state.titleId ? state.libraryId : undefined),
    reload: () => {void controller.reload();}, more: () => {void controller.open(state.libraryId, undefined, true);},
    approveSeason: () => run(() => controller.approve(season?.number)), confirm, setConfirm,
    redetect: () => {setConfirm(false); run(() => controller.redetect());},
    changeKind: (entry: SegmentReviewEntry, marker: SegmentReviewMarker, kind: SegmentKind) =>
      controller.edit(entry, {action: 'edit', markerId: marker.id, kind, startUS: marker.startUS, endUS: marker.endUS}),
    changeBounds: (entry: SegmentReviewEntry, marker: SegmentReviewMarker, bounds: {startUS: string; endUS: string}) =>
      controller.edit(entry, {action: 'edit', markerId: marker.id, kind: marker.kind, ...bounds}),
    approve: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => run(() => controller.edit(entry, {action: 'approve', markerId: marker.id})),
    dismiss: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => run(() => controller.edit(entry, {action: 'dismiss', markerId: marker.id})),
    watch: (entry: SegmentReviewEntry, marker: SegmentReviewMarker, fromUS: string) => run(() => watch(entry, marker, fromUS)),
    add: (entry: SegmentReviewEntry) => {
      setAdding(entry); setKind('intro'); setStart('0:00'); setEnd(segmentClock(String(Math.min(1e6, Number(entry.durationUS)))));
    },
    adding, closeAdd: () => setAdding(undefined), kind, setKind, start, setStart, end, setEnd, valid,
    save: () => {if (adding && valid) run(async () => {await controller.edit(adding, {action: 'create', kind, startUS: startUS!, endUS: endUS!}); setAdding(undefined);});},
  };
}

export function useSegmentPoster({id, libraryId, context, lifetime, runtime}: Pick<NativeSettingsPanelDeps, 'context' | 'lifetime'> & {
  id: string; libraryId: string; runtime: NativeShellRuntime;
}) {
  const [path, setPath] = useState<string>();
  useEffect(() => {
    const read = new AbortController();
    setPath(undefined);
    void lifetime.request(signal => readCatalogueItemEncodedResponse(context.api, id, signal).then(parseMediaItem), read.signal).then(item => {
      if (!read.signal.aborted && item.id === id && item.libraryId === libraryId) setPath(item.posterUrl);
    }, () => { /* Missing artwork retains the kit's empty surface. */ });
    return () => read.abort();
  }, [context, id, libraryId, lifetime]);
  return runtime.artwork(path);
}
