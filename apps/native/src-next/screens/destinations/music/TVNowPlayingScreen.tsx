import React, {useCallback, useEffect, useId, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {sameFence} from '../../../../../../packages/player-core/src';
import {Layer} from '../../../kit/shell/Layer';
import {TVAudioPlayerPage} from '../../../pages/tv/player/AudioPlayerPage';
import {provideTVLayerClaim, requestNativeFocus} from '../../../kit/focus';
import {Menu, SidePanel} from '../../../kit/overlays';
import {commandAllowed, dockCommand, playbackPosition, seekBounds} from '../../../kit/dock/model';
import {shellLayout} from '../../../shell/model';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {registerOverlayKeys} from '../../../platform/overlay-keys';
import {NativeWatchEqualizer, type NativeEqualizerCopy} from '../../watch/Equalizer';
import {MusicSignalLine, MusicSignalSheet} from './MusicSignal';
import {NativeListeningNotice} from './ListeningNotice';
import {NowPlayingQueue, nowPlayingQueueRows, nowPlayingQueueFocusScope, nowPlayingQueueEditPort} from './NowPlayingQueue';
import {TVMusicFocus, TVMusicSeek} from './TVMusicFocus';
import type {NowPlayingScreenProps} from './NowPlayingScreen';
import {useTVQueuePolicy} from './TVQueuePolicy';
import {TVPageGroup, TVPageTransition, useTVPageMotion} from '../../../tv/layout/TVPageMotion';
import {registerBack} from '../../../kit/navigation/back';

/** tv7: one authored 1920×1080 canvas, including the queue. The window never
 * switches this television to the tablet composition at a lower render size. */
export function TVNowPlayingScreen(props: NowPlayingScreenProps & {equalizerCopy: NativeEqualizerCopy}) {
  return <TVPageTransition open={props.tvOpen ?? true} onExited={props.onTVExited ?? (() => {})}><TVNowPlayingContent {...props}/></TVPageTransition>;
}

function TVNowPlayingContent({occupant, queue, artwork, width, height, onAction, onQueuePlay, onAddToPlaylist, queuePolicy, policyLifetime, queueEdit, onClose, signal, notice,
  reportError = () => {}, testID = 'now-playing', lyrics, equalizerCopy}: NowPlayingScreenProps & {equalizerCopy: NativeEqualizerCopy}) {
  const t = defaultI18n.t, id = useId(), canvas = shellLayout(width, height, true);
  const page = useTVPageMotion();
  const [panel, setPanel] = useState<'signal' | 'eq' | 'lyrics' | null>(null), [more, setMore] = useState(false);
  const signalRef = useRef<View>(null), moreRef = useRef<View>(null), lyricsRef = useRef<View>(null);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const policy = useTVQueuePolicy({fence: occupant.playback.fence, queue, policy: queuePolicy, reportError, testID});
  const restoreFrame = useRef<number | undefined>(undefined), latest = useRef({panel, fence: occupant.playback.fence, visible: page.open && page.ready});
  latest.current = {panel, fence: occupant.playback.fence, visible: page.open && page.ready};
  const closeEqualizer = useCallback(() => {
    const fence = occupant.playback.fence;
    setPanel(null);
    if (restoreFrame.current !== undefined) cancelAnimationFrame(restoreFrame.current);
    restoreFrame.current = requestAnimationFrame(() => {
      if (latest.current.visible && latest.current.panel === null && sameFence(latest.current.fence, fence))
        (moreRef.current as unknown as {requestTVFocus?: () => void} | null)?.requestTVFocus?.();
    });
    return true;
  }, [occupant.playback.fence]);
  useEffect(() => () => {if (restoreFrame.current !== undefined) cancelAnimationFrame(restoreFrame.current);}, []);
  useEffect(() => {
    if (panel !== 'eq') return;
    const releaseBack = registerBack(closeEqualizer, {scope: 'overlay'});
    const release = registerOverlayKeys(key => key === 'escape' || key === 'menu' ? closeEqualizer() : false);
    return () => {releaseBack(); release();};
  }, [panel, closeEqualizer]);
  const {presentation: p, playback} = occupant;
  const position = playbackPosition(occupant), bounds = seekBounds(occupant), span = bounds ? bounds.end - bounds.start : p.durationSeconds;
  const artist = p.identity.subtitle.filter(part => !!part.text);
  const album = (p.album ?? []).filter(part => !!part.text);
  // Right names a queue containing a real readable native focus stop.
  const queueRegion = queue && nowPlayingQueueRows(queue).some(row => !row.current) ? `${id}.queue` : undefined;
  const actionsRegion = lyrics || onAddToPlaylist || equalizer.supported ? `${id}.actions` : undefined;
  const enabled = page.open && page.ready && !panel && !more;
  const currentEntry = queue?.queue.entries.find(entry => entry.id === queue.queue.currentEntryId);
  const currentItem = queue?.items.find(item => item.entryId === queue.queue.currentEntryId);
  // An album in the metadata alone does not say where this queue came from.
  const fromAlbum = currentEntry && !currentEntry.hidden && currentEntry.sourceContext.kind === 'album'
    && currentEntry.sourceContext.id === currentItem?.albumId ? currentItem.album : undefined;
  // W-9: the player is the top layer on TV; everything under it is covered and cannot hold focus,
  // so it takes focus itself as it opens: Play/Pause, or (at the end of a track, when that is not
  // available) the first control that is. Nothing else asks for it, and focus otherwise stayed on
  // the covered page underneath, with nothing visible focused.
  const previousRef = useRef<View>(null), toggleRef = useRef<View>(null), nextRef = useRef<View>(null), playlistRef = useRef<View>(null);
  const shown = page.open && page.ready;
  const opening = useRef({toggle: false, next: false, previous: false});
  opening.current = {toggle: playback.playing ? p.capabilities.pause : p.capabilities.play, next: p.capabilities.next, previous: p.capabilities.previous};
  // TVF-A1: the TV layer stack claims focus for this layer when it goes on top, and asks again as its native
  // views attach (a request made before they are attached does nothing: 2eec0520). This screen only says which
  // control it opens on; nothing here loops or counts frames.
  const shownNow = useRef(shown); shownNow.current = shown;
  useLayoutEffect(() => provideTVLayerClaim('listening', () => {
    if (!shownNow.current) return false;
    const enabled = opening.current;
    const order = [enabled.toggle && toggleRef, enabled.next && nextRef, enabled.previous && previousRef, lyricsRef, playlistRef, moreRef];
    for (const ref of order) if (ref && ref.current && requestNativeFocus(ref.current as never)) return true;
    return false;
  }), []);
  const act = (action: Parameters<typeof dockCommand>[1]) => {if (page.open && page.ready && !panel && !more && commandAllowed(occupant, action)) onAction(dockCommand(occupant, action));};
  const close = () => setPanel(null);
  return <TVMusicFocus id={id} visible={page.open && !panel && !more} interactive={page.ready} entry="toggle" onClose={onClose}>
    <View testID={testID} pointerEvents={page.ready ? 'auto' : 'none'} accessibilityElementsHidden={!page.ready} importantForAccessibility={page.ready ? 'auto' : 'no-hide-descendants'} style={{position: 'absolute', inset: 0}}>
      <Layer kind="audio" art={p.ambient} style={{position: 'absolute', inset: 0}}/>
      <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
        transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
        <TVPageGroup index={0} style={{flex: 1}}>
          <TVAudioPlayerPage title={p.identity.title} icon={occupant.mode === 'audiobook' ? 'i-book' : 'i-music'}
            artwork={p.artwork ?? artwork?.(currentItem?.posterUrl)}
            context={fromAlbum ? t('next.music.playingFrom', {source: ''}).trim() : undefined} source={fromAlbum}
            byline={[artist.map(part => part.text).join(' '), album.map(part => part.text).join(' ') || currentItem?.album || '']} onClose={onClose}
            more={equalizer.supported ? {icon: 'i-more', label: t('next.controls.more'), focusId: 'more', testID: `${testID}-more`,
              anchorRef: moreRef, onPress: () => setMore(true)} : undefined}
            signal={signal && <MusicSignalLine view={signal} focusRef={signalRef} onPress={() => setPanel('signal')}/>}
            scrubber={<TVMusicSeek occupant={occupant} onAction={onAction} enabled={enabled} testID={`${testID}-scrubber`}/>}
            times={{position: clockText(Math.max(0, position - (bounds?.start ?? 0))) ?? '', end: clockText(span) ?? ''}}
            transport={[
              policy.shuffle,
              {icon: 'i-prev', label: t('player.previous'), focusId: 'previous', anchorRef: previousRef,
                testID: `${testID}-previous`, disabled: !p.capabilities.previous, onPress: () => act('previous')},
              {icon: playback.playing ? 'i-pause' : 'i-play', label: t(playback.playing ? 'action.pause' : 'action.play'),
                focusId: 'toggle', anchorRef: toggleRef, testID: `${testID}-toggle`, disabled: playback.playing ? !p.capabilities.pause : !p.capabilities.play,
                onPress: () => act(playback.playing ? 'pause' : 'play')},
              {icon: 'i-next', label: t('player.next'), focusId: 'next', anchorRef: nextRef,
                testID: `${testID}-next`, disabled: !p.capabilities.next, onPress: () => act('next')},
              policy.repeat,
            ]}
            lyrics={lyrics ? {icon: 'i-lyrics', label: t('player.lyrics'), focusId: 'lyrics', testID: `${testID}-lyrics`,
              anchorRef: lyricsRef, onPress: () => setPanel('lyrics')} : undefined}
            playlist={onAddToPlaylist ? {icon: 'i-listplus', label: t('entry.addToPlaylist'), focusId: 'playlist',
              testID: `${testID}-playlist`, anchorRef: playlistRef, onPress: onAddToPlaylist} : undefined}
            tvFocus={{
              signal: {id: `${id}.signal`, entry: 'music-signal-line', enabled, edges: {down: `${id}.progress`, right: queueRegion}},
              seek: {id: `${id}.progress`, entry: 'seek', enabled, edges: {up: signal ? `${id}.signal` : undefined, down: `${id}.transport`, right: queueRegion}},
              transport: {id: `${id}.transport`, entry: 'toggle', enabled, orientation: 'horizontal', edges: {up: `${id}.progress`, down: actionsRegion, right: queueRegion}},
              actions: actionsRegion ? {id: actionsRegion, entry: lyrics ? 'lyrics' : onAddToPlaylist ? 'playlist' : 'more',
                enabled, orientation: 'horizontal', edges: {up: `${id}.transport`, right: queueRegion}} : undefined,
            }}
            panelTitle={queue ? t('next.music.upNext') : undefined}
            panel={queue && <NowPlayingQueue testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork}
              queueEdit={nowPlayingQueueEditPort(queue, occupant.playback.fence, queueEdit)}
              menuScope={{fenceKey: JSON.stringify(occupant.playback.fence), lease: queueEdit?.lease ?? policyLifetime}}
              interactive={enabled} focusScopeKey={nowPlayingQueueFocusScope(occupant.playback.fence)}
              regionId={`${id}.queue`} edges={{left: `${id}.transport`}}/>}/>
        </TVPageGroup>
      </View>
      <Menu testID={`${id}.more`} open={more} onOpenChange={setMore} title={t('next.controls.more')} presentation="sideSheet" returnFocusRef={moreRef} restoreFocus={panel !== 'eq'}
        items={equalizer.supported ? [{id: 'eq', label: t('next.media.eq.title'), icon: 'i-gauge', onSelect: () => {setMore(false); setPanel('eq');}}] : []}/>
      {panel === 'signal' && signal && <MusicSignalSheet view={signal} contextTitle={p.identity.title} onClose={close} returnFocusRef={signalRef}/>}
      {panel === 'lyrics' && lyrics && <SidePanel testID={`${id}.lyrics`} open title={t('player.lyrics')} width={640} topInset={0} onOpenChange={open => {if (!open) close();}} returnFocusRef={lyricsRef}>{lyrics}</SidePanel>}
      {panel === 'eq' && <Layer style={{position: 'absolute', inset: 0}}>
        <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
          transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
          <NativeWatchEqualizer focusScope={`${id}.eq`} onBack={closeEqualizer}
            copy={equalizerCopy} reportError={reportError} store={nativeEqualizer}/>
        </View>
      </Layer>}
      {notice && <NativeListeningNotice notice={notice} persistent/>}
    </View>
  </TVMusicFocus>;
}
