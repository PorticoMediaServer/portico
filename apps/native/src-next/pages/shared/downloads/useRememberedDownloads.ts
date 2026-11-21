import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {registerBack} from '../../../kit/navigation/back';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {videoTrackName, videoTrackWords} from '../../../../../../packages/client-core/src/video-track-labels';
import type {AudioStream, SubtitleStream} from '../../../../../../packages/client-core/src/playback-v1/types';
import type {OfflineEntry, OfflineSnapshot} from '../../../platform/offline-downloads';
import type {RememberedDownloadsOpening} from '../../../shell/remembered-downloads-opening';
import type {createRememberedReadingOwner} from '../../../shell/remembered-reading-owner';
import {downloadEntryIdentity} from '../../../screens/downloads/presentation';
import {createDownloadInventory} from '../../../screens/downloads/inventory';

const empty: Pick<OfflineSnapshot, 'entries'> = Object.freeze({entries: Object.freeze([])});
type Opening = {source: RememberedDownloadsOpening; active: boolean; abort: AbortController; flight?: object};
export type RememberedRun = (action: (lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<void>) => void;

/** The downloads a phone still plays while it can't reach its server (states 4a's "Your downloads still play"). No normal
 * shell/runtime impersonation: this view borrows only the root's exact offline opening and its one native receipt-owned
 * surface. */
export function useRememberedDownloads({source, onBack}: {source: RememberedDownloadsOpening; onBack: () => void}) {
  const owner = useRef<Opening | undefined>(undefined);
  const [committed, setCommitted] = useState<Opening>(), [busy, setBusy] = useState(false), [failure, setFailure] = useState<unknown>();
  const [reading, setReading] = useState<ReturnType<typeof createRememberedReadingOwner> | undefined>();
  if (owner.current && owner.current.source !== source) {owner.current.active = false; owner.current.abort.abort();}
  useLayoutEffect(() => {
    const opening: Opening = {source, active: true, abort: new AbortController()}; owner.current = opening; setCommitted(opening);
    return () => {opening.active = false; opening.abort.abort(); if (owner.current === opening) owner.current = undefined;};
  }, [source]);
  const subscribe = useCallback((listener: () => void) => {try {return source.current() ? source.device.subscribe(listener) : () => {};} catch {return () => {};}}, [source]);
  const read = useCallback((): Pick<OfflineSnapshot, 'entries'> => {try {return source.current() ? source.device.getSnapshot() : empty;} catch {return empty;}}, [source]);
  const inventory = useMemo(() => {
    const admitted = (entry: OfflineEntry) => {try {const kind = entry.kind ?? entry.info?.kind ?? '';
      return entry.state === 'ready' && ((!!source.player && ['movie', 'episode', 'song', 'audiobook_file'].includes(kind))
        || (['ebook', 'comic'].includes(kind) && !!source.canRead?.(entry))) && source.device.playabilityReason(entry) === null;
    } catch {return false;}};
    return createDownloadInventory({subscribe, getSnapshot: read}, {identity: downloadEntryIdentity,
      membershipKey: entry => [entry.state, entry.kind, entry.info?.kind, admitted(entry)], entryFence: admitted,
      project: entries => entries.filter(admitted).map(downloadEntryIdentity)});
  }, [source, subscribe, read]);
  const entries = useSyncExternalStore(inventory.subscribe, inventory.getMembership, inventory.getMembership);
  const rendered = committed;
  const current = () => !!rendered?.active && rendered.source === source && owner.current === rendered && !rendered.abort.signal.aborted && source.current();
  const run: RememberedRun = action => {
    if (!current() || !rendered || rendered.flight) return;
    const flight = {}; rendered.flight = flight; setBusy(true); setFailure(undefined);
    const active = () => current() && rendered.flight === flight;
    if (!active()) return;
    void action({signal: rendered.abort.signal, isCurrent: active}).catch(error => {if (active()) setFailure(error);})
      .finally(() => {if (active()) {rendered.flight = undefined; setBusy(false);}});
  };
  const play = (entry: OfflineEntry) => {const kind = entry.kind ?? entry.info?.kind ?? '';
    if (['ebook', 'comic'].includes(kind)) {if (!current() || !source.reading) return; try {setFailure(undefined); setReading(source.reading(entry));} catch (error) {if (current()) setFailure(error);} return;}
    const player = source.player; if (player) run(lifetime => player.open(entry, lifetime));};
  const back = () => {if (!current() || !rendered) return; rendered.active = false; rendered.abort.abort();
    const fence = source.player?.fence(); if (fence) source.player?.close(fence); onBack();};
  // Android Back leaves the list for the recovery page, as the list's own Back does (the reader, open over it, answers first).
  const backRef = useRef(back); backRef.current = back;
  useEffect(() => registerBack(() => {backRef.current(); return true;}, {scope: 'surface'}), []);
  return {inventory, entries, busy, current, run, play, back, reading, closeReading: () => {if (current()) setReading(undefined);},
    failure: failure ? presentError(failure, 'home').body : undefined};
}

type Player = NonNullable<RememberedDownloadsOpening['player']>;
const playing = ['starting', 'ready'];

/** The receipt-only player above the list: its surface, Play or Pause, where it is, and the audio and subtitle choices the
 * download carries, each change run through the page's one flight. */
export function useRememberedPlayer({player, busy, current, run}: {player: Player; busy: boolean; current: () => boolean; run: RememberedRun}) {
  const t = defaultI18n.t;
  const state = useSyncExternalStore(player.subscribe, player.getSnapshot, player.getSnapshot), fence = player.fence();
  const tracks = state.localTracks, words = videoTrackWords(t, defaultI18n.locale);
  const name = (track: AudioStream | SubtitleStream, index: number) => {
    const value = videoTrackName(track, words);
    return {label: value.first || t('next.kit.track', {number: index + 1}), description: value.rest || undefined};
  };
  const seconds = (value: number) => new Intl.NumberFormat(defaultI18n.locale, {minimumFractionDigits: 1, maximumFractionDigits: 1}).format(value);
  const changing = busy || !playing.includes(state.phase);
  const timing = fence && tracks?.subtitleTrackId && tracks.subtitleTimingSupported ? {
    label: t('player.opt.timing'), value: t('player.opt.offsetSeconds', {seconds: seconds(tracks.subtitleOffsetMs / 1000)}),
    steps: [-500, 500].map(delta => ({id: String(delta), label: `${delta < 0 ? '−' : '+'}${seconds(.5)}`,
      accessibilityLabel: t(delta < 0 ? 'player.opt.earlier' : 'player.opt.later'),
      disabled: changing || Math.abs(tracks.subtitleOffsetMs + delta) > 600_000,
      onPress: () => {if (current()) run(async () => player.change(fence, {subtitleTrackId: tracks.subtitleTrackId, subtitleOffsetMs: tracks.subtitleOffsetMs + delta}));}})),
  } : undefined;
  return {
    Surface: player.Surface,
    toggle: fence ? {label: t(state.phase !== 'ended' && state.intent === 'playing' ? 'action.pause' : 'action.play'),
      disabled: busy || !['starting', 'ready', 'ended'].includes(state.phase), onPress: () => {if (!current()) return;
        if (state.phase === 'ended') run(lifetime => player.replay(fence, lifetime));
        else if (state.intent === 'playing') player.pause(fence); else player.resume(fence);
      }, position: `${Math.floor(state.positionSeconds)} / ${Math.floor(state.session?.duration ?? 0)}`} : undefined,
    audio: fence && tracks && tracks.audio.length > 1 ? {label: t('title.audio'), value: tracks.audioTrackId ?? '', disabled: changing,
      options: tracks.audio.map((track, index) => ({value: track.id, ...name(track, index)})),
      onValueChange: (id: string) => {if (current()) run(async () => player.change(fence, {audioTrackId: id}));}} : undefined,
    subtitles: fence && tracks && tracks.subtitles.length > 0 ? {label: t('player.opt.subtitles'), value: tracks.subtitleTrackId ?? '', disabled: changing,
      options: [{value: '', label: t('player.opt.off')}, ...tracks.subtitles.map((track, index) => ({value: track.id, ...name(track, index), disabled: track.unavailable}))],
      onValueChange: (id: string) => {if (current()) run(async () => player.change(fence, {subtitleTrackId: id || null}));}} : undefined,
    subtitleError: fence && tracks?.subtitleError ? t('player.opt.unavailable') : undefined,
    timing, error: state.error || undefined,
  };
}

/** The account entry's shortcut to the same downloads while recovery is on screen. */
export function useRememberedShortcut(source: RememberedDownloadsOpening) {
  const owner = useRef<{source: RememberedDownloadsOpening; active: boolean} | undefined>(undefined);
  const [opening, setOpening] = useState<{source: RememberedDownloadsOpening; active: boolean}>();
  if (owner.current && owner.current.source !== source) owner.current.active = false;
  useLayoutEffect(() => {const ticket = {source, active: true}; owner.current = ticket; setOpening(ticket);
    return () => {ticket.active = false; if (owner.current === ticket) owner.current = undefined;};}, [source]);
  const snapshot = useSyncExternalStore(source.entry.subscribe, source.entry.getSnapshot, source.entry.getSnapshot);
  return !snapshot.available || !source.current() ? undefined
    : () => {if (opening?.active && opening.source === source && owner.current === opening && source.current()) source.entry.open();};
}
