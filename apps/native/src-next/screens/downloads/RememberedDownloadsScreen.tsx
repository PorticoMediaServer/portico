import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {videoTrackName, videoTrackWords} from '../../../../../packages/client-core/src/video-track-labels';
import type {AudioStream, SubtitleStream} from '../../../../../packages/client-core/src/playback-v1/types';
import type {OfflineEntry, OfflineSnapshot} from '../../platform/offline-downloads';
import type {RememberedDownloadsOpening} from '../../shell/remembered-downloads-opening';
import {Button, RichSelect} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {downloadEntryIdentity} from './presentation';
import {createDownloadInventory} from './inventory';
import type {createRememberedReadingOwner} from '../../shell/remembered-reading-owner';
import {StandardBackdrop} from '../../kit/artwork';

const empty: Pick<OfflineSnapshot, 'entries'> = Object.freeze({entries: Object.freeze([])});
type Opening = {source: RememberedDownloadsOpening; active: boolean; abort: AbortController; flight?: object};
/** No normal shell/runtime impersonation: this view borrows only the root's
 * exact offline opening and its one native receipt-owned surface. */
export function RememberedDownloadsScreen({source, onBack}: {source: RememberedDownloadsOpening; onBack: () => void}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions(), t = defaultI18n.t;
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
  const run = (action: (lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<void>) => {
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
  if (reading && source.noticeCopy) {const ReadingScreen = (require('./RememberedReadingScreen') as typeof import('./RememberedReadingScreen')).RememberedReadingScreen;
    return <ReadingScreen reading={reading} noticeCopy={source.noticeCopy} current={current} onBack={() => {if (current()) setReading(undefined);}}/>;}
  return <View testID="remembered-downloads" style={{flex: 1, paddingTop: insets.top}}><StandardBackdrop/>
    <FlatList data={entries} keyExtractor={id => id} initialNumToRender={12} maxToRenderPerBatch={8} windowSize={5}
      contentContainerStyle={{padding: 24, paddingBottom: Math.max(24, insets.bottom)}}
      ItemSeparatorComponent={() => <View style={{height: 16}}/>}
      ListHeaderComponent={<View style={{gap: 16, marginBottom: 16}}>
        <Button variant="text" onPress={back}>{t('action.back')}</Button>
        <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{t('downloads.title')}</Text>
        {source.player ? <RememberedPlayer player={source.player} width={dimensions.width} busy={busy} current={current} run={run}/> : null}
        {!!failure && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{presentError(failure, 'home').body}</Text>}
      </View>}
      renderItem={({item}) => <RememberedDownloadRow id={item} inventory={inventory} busy={busy} play={play}/>}
      ListEmptyComponent={<Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('downloads.emptyTitle')}</Text>}/>

  </View>;
}

function RememberedDownloadRow({id, inventory, busy, play}: {id: string; inventory: ReturnType<typeof createDownloadInventory<OfflineEntry, string[]>>;
  busy: boolean; play: (entry: OfflineEntry) => void}) {
  const read = useCallback(() => inventory.getEntry(id), [inventory, id]), selected = useSyncExternalStore(inventory.subscribe, read, read);
  useEffect(() => () => inventory.release(id), [inventory, id]);
  return selected ? <Button variant="glass" disabled={busy} onPress={() => play(selected.entry)}>{selected.entry.title}</Button> : null;
}

type Player = NonNullable<RememberedDownloadsOpening['player']>;
function RememberedPlayer({player, width, busy, current, run}: {player: Player; width: number; busy: boolean; current: () => boolean;
  run: (action: (lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<void>) => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  const state = useSyncExternalStore(player.subscribe, player.getSnapshot, player.getSnapshot), fence = player.fence(), Surface = player.Surface;
  const tracks = state.localTracks, words = videoTrackWords(t, defaultI18n.locale);
  const name = (track: AudioStream | SubtitleStream, index: number) => {
    const value = videoTrackName(track, words);
    return {label: value.first || t('next.kit.track', {number: index + 1}), description: value.rest || undefined};
  };
  return <>
    <View style={{height: Math.round(width * 9 / 16), backgroundColor: theme.colors.raised}}><Surface/></View>
    {fence && <View style={{flexDirection: 'row', gap: 12}}>
      <Button variant="glass" disabled={busy || !['starting', 'ready', 'ended'].includes(state.phase)} onPress={() => {if (!current()) return;
        if (state.phase === 'ended') run(lifetime => player.replay(fence, lifetime));
        else if (state.intent === 'playing') player.pause(fence); else player.resume(fence);
      }}>{t(state.phase !== 'ended' && state.intent === 'playing' ? 'action.pause' : 'action.play')}</Button>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{Math.floor(state.positionSeconds)} / {Math.floor(state.session?.duration ?? 0)}</Text>
    </View>}
    {fence && tracks && <>
      {tracks.audio.length > 1 && <RichSelect testID="remembered-player-audio" label={t('title.audio')} value={tracks.audioTrackId ?? ''}
        disabled={busy || !['starting', 'ready'].includes(state.phase)} options={tracks.audio.map((track, index) => ({value: track.id, ...name(track, index)}))}
        onValueChange={id => {if (current()) run(async () => player.change(fence, {audioTrackId: id}));}}/>}
      {tracks.subtitles.length > 0 && <RichSelect testID="remembered-player-subtitles" label={t('player.opt.subtitles')} value={tracks.subtitleTrackId ?? ''}
        disabled={busy || !['starting', 'ready'].includes(state.phase)} options={[{value: '', label: t('player.opt.off')}, ...tracks.subtitles.map((track, index) => ({value: track.id, ...name(track, index), disabled: track.unavailable}))]}
        onValueChange={id => {if (current()) run(async () => player.change(fence, {subtitleTrackId: id || null}));}}/>}
      {tracks.subtitleError && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('player.opt.unavailable')}</Text>}
      {tracks.subtitleTrackId && tracks.subtitleTimingSupported && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('player.opt.timing')}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('player.opt.offsetSeconds', {seconds: new Intl.NumberFormat(defaultI18n.locale, {minimumFractionDigits: 1, maximumFractionDigits: 1}).format(tracks.subtitleOffsetMs / 1000)})}</Text>
        {[-500, 500].map(delta => <Button key={delta} variant="glass" disabled={busy || !['starting', 'ready'].includes(state.phase) || Math.abs(tracks.subtitleOffsetMs + delta) > 600_000}
          accessibilityLabel={t(delta < 0 ? 'player.opt.earlier' : 'player.opt.later')} onPress={() => {if (current()) run(async () => player.change(fence,
            {subtitleTrackId: tracks.subtitleTrackId, subtitleOffsetMs: tracks.subtitleOffsetMs + delta}));}}>{`${delta < 0 ? '−' : '+'}${new Intl.NumberFormat(defaultI18n.locale, {minimumFractionDigits: 1, maximumFractionDigits: 1}).format(.5)}`}</Button>)}
      </View>}
    </>}
    {state.error && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{state.error}</Text>}
  </>;
}

export function RememberedDownloadsShortcut({source}: {source: RememberedDownloadsOpening}) {
  const owner = useRef<{source: RememberedDownloadsOpening; active: boolean} | undefined>(undefined);
  const [opening, setOpening] = useState<{source: RememberedDownloadsOpening; active: boolean}>();
  if (owner.current && owner.current.source !== source) owner.current.active = false;
  useLayoutEffect(() => {const ticket = {source, active: true}; owner.current = ticket; setOpening(ticket);
    return () => {ticket.active = false; if (owner.current === ticket) owner.current = undefined;};}, [source]);
  const snapshot = useSyncExternalStore(source.entry.subscribe, source.entry.getSnapshot, source.entry.getSnapshot);
  if (!snapshot.available || !source.current()) return null;
  return <Button variant="glass" onPress={() => {if (opening?.active && opening.source === source && owner.current === opening && source.current()) source.entry.open();}}>{defaultI18n.t('downloads.title')}</Button>;
}
