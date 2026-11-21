import {useEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {activeLyricLine, lyricSeekPosition, type LyricsService, type LyricsSnapshot, type LyricsTarget} from '../../../../../../packages/client-core/src/lyrics';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {committedPaintScope, useCommittedPaintPublication} from '../../../kit/overlays/CommittedPaint';
import type {ListeningLyricsPaintData} from '../../../kit/player/ListeningPanePaint';

const t = defaultI18n.t;
const emptySnapshot: LyricsSnapshot = Object.freeze({data: null, candidates: Object.freeze([]), warnings: Object.freeze([]), preview: null, busy: false, error: '', generation: 0});
const noService = () => emptySnapshot;
const noSubscription = () => () => {};

/** The song's own synced lyrics, from the server's lyric resource for this
 * session and source. The reading is bound to the accepted playback position,
 * so the highlighted line follows the engine rather than a wall clock, and a
 * line seeks only through the real playback command. The viewer's "Synced
 * lyrics" preference decides whether this is offered at all. */
export type NowPlayingLyricsProps = Readonly<{
  read: (target: LyricsTarget) => Readonly<{enabled: boolean; service: LyricsService}> | undefined;
  target: LyricsTarget;
  positionSeconds: number;
  playing: boolean;
  onSeek: (seconds: number) => void;
  /** Touch publisher only; its pure display leaf lives in the retained page. */
  paintOwner?: object;
  paintScope?: string;
  testID?: string;
}>;

export function useNowPlayingLyrics({read, target, positionSeconds, playing, onSeek, paintOwner, paintScope = ''}: NowPlayingLyricsProps) {
  const owned = useMemo(() => {try {return read(target) ?? undefined;} catch {return undefined;}}, [read, target]);
  const service = owned?.service;
  const snapshot = useSyncExternalStore(service?.subscribe ?? noSubscription, service?.getSnapshot ?? noService);
  const live = useRef(playing); live.current = playing;
  useEffect(() => {
    if (!service) return;
    // One read when the panel opens, then a slow refresh while the song is playing;
    // a paused song keeps the lyrics it already has.
    const refresh = () => {if (live.current) void service.refresh();};
    void service.refresh();
    const timer = repeat(async () => {refresh();}, {everyMs: 30_000, backoff: {baseMs: 30_000, maxMs: 30_000, jitter: 'none'}, immediate: false});
    return () => {timer.stop(); service.dispose();};
  }, [service]);
  const selected = snapshot.data?.selection.resource, document = selected?.document, source = snapshot.data?.source;
  const current = document ? activeLyricLine(document, positionSeconds, selected?.offsetMs ?? 0, source?.startSeconds ?? 0) : -1;
  const paintSemantic = useMemo(() => committedPaintScope(paintOwner, paintScope, service, document, selected, source), [paintOwner, paintScope, service, document, selected, source]);
  const resources = useMemo(() => !document ? snapshot.data?.resources.filter(resource => !resource.deleted) ?? [] : [], [document, snapshot.data?.resources]);
  const resourceLabels = useMemo(() => resources.map(resource => ({id: resource.id, label: resource.provenance.label || resource.language})), [resources]);
  const seeks = useMemo(() => document?.lines.map(line => source ? lyricSeekPosition(line, selected?.offsetMs ?? 0, source) : null) ?? [], [document, selected?.offsetMs, source]);
  const lines = useMemo(() => document?.lines.map((line, index) => ({index, text: line.text, active: current >= 0 && index === current, seekable: typeof seeks[index] === 'number'})), [document, seeks, current]);
  // A progress render may supply a fresh closure over the same pinned command.
  // Keep the original command for this document/source lifetime; an old pane
  // must never borrow a successor's callback.
  const actions = useMemo(() => ({
    retry: () => {if (service) void service.refresh();},
    choose: (id: string) => {const resource = resources.find(value => value.id === id);if (resource && service) void service.choose(resource);},
    seek: (index: number) => {const at = seeks[index];if (typeof at === 'number' && Number.isFinite(at)) onSeek(at);},
    // The semantic token includes the original service, source and document.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [service, resources, seeks, paintSemantic]);
  const display: ListeningLyricsPaintData | undefined = useMemo(() => owned?.enabled ? {
    error: snapshot.error || undefined,
    messageOnly: !service,
    message: !service ? t('player.noLyrics') : !document && !snapshot.busy && !snapshot.error
      ? t(snapshot.data?.resources.length ? 'player.chooseLyrics' : 'player.noLyrics') : undefined,
    resources: resourceLabels,
    lines,
    activeIndex: current,
  } : undefined, [owned?.enabled, snapshot.error, snapshot.busy, snapshot.data?.resources.length, service, document, resourceLabels, lines, current]);
  const publication = useMemo(() => display ? {data: display, actions} : undefined, [display, actions]);
  useCommittedPaintPublication(paintOwner, 'listening-lyrics', paintScope, paintSemantic, publication);
  return paintOwner ? undefined : publication;
}
