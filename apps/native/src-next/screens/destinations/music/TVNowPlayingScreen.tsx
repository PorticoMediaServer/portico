import React, {useCallback, useEffect, useId, useRef, useState, useSyncExternalStore} from 'react';
import {BackHandler, ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {sameFence} from '../../../../../../packages/player-core/src';
import {AmbientArt, Artwork, StandardBackdrop} from '../../../kit/artwork';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {Menu, SidePanel} from '../../../kit/overlays';
import {useTheme, nativeType} from '../../../kit/theme';
import {commandAllowed, dockCommand, playbackPosition, seekBounds} from '../../../kit/dock/model';
import {shellLayout} from '../../../shell/model';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {registerOverlayKeys} from '../../../platform/overlay-keys';
import {NativeWatchEqualizer, type NativeEqualizerCopy} from '../../watch/Equalizer';
import {MusicSignalLine, MusicSignalSheet} from './MusicSignal';
import {NativeListeningNotice} from './ListeningNotice';
import {NowPlayingQueue, nowPlayingQueueRows, nowPlayingQueueFocusScope, nowPlayingQueueEditPort} from './NowPlayingQueue';
import {MusicFocusRegion, TVMusicFocus, TVMusicSeek} from './TVMusicFocus';
import type {NowPlayingScreenProps} from './NowPlayingScreen';
import {useTVQueuePolicy} from './TVQueuePolicy';
import {TVPageGroup, TVPageTransition, useTVPageMotion} from '../../../tv/layout/TVPageMotion';

/** tv7: one authored 1920×1080 canvas, including the queue. The window never
 * switches this television to the tablet composition at a lower render size. */
export function TVNowPlayingScreen(props: NowPlayingScreenProps & {equalizerCopy: NativeEqualizerCopy}) {
  return <TVPageTransition open={props.tvOpen ?? true} onExited={props.onTVExited ?? (() => {})}><TVNowPlayingContent {...props}/></TVPageTransition>;
}

function TVNowPlayingContent({occupant, queue, artwork, width, height, onAction, onQueuePlay, onAddToPlaylist, queuePolicy, policyLifetime, queueEdit, onClose, signal, notice,
  reportError = () => {}, testID = 'now-playing', lyrics, equalizerCopy}: NowPlayingScreenProps & {equalizerCopy: NativeEqualizerCopy}) {
  const theme = useTheme(), t = defaultI18n.t, id = useId(), canvas = shellLayout(width, height, true);
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
    const back = BackHandler.addEventListener('hardwareBackPress', closeEqualizer);
    const release = registerOverlayKeys(key => key === 'escape' || key === 'menu' ? closeEqualizer() : false);
    return () => {back.remove(); release();};
  }, [panel, closeEqualizer]);
  const {presentation: p, playback} = occupant;
  const type = (size: number, weight: 400 | 600 | 700 | 800 = 400, tracking = 0) => nativeType({size, weight, tracking, lineHeight: 'normal'}, theme.textScale);
  const position = playbackPosition(occupant), bounds = seekBounds(occupant), span = bounds ? bounds.end - bounds.start : p.durationSeconds;
  const artist = p.identity.subtitle.filter(part => !!part.text);
  const album = (p.album ?? []).filter(part => !!part.text);
  // Right names a queue containing a real readable native focus stop.
  const queueRegion = queue && nowPlayingQueueRows(queue).some(row => !row.current) ? `${id}.queue` : undefined;
  const actionsRegion = lyrics || onAddToPlaylist || equalizer.supported ? `${id}.actions` : undefined;
  const currentEntry = queue?.queue.entries.find(entry => entry.id === queue.queue.currentEntryId);
  const currentItem = queue?.items.find(item => item.entryId === queue.queue.currentEntryId);
  // An album in the metadata alone does not say where this queue came from.
  const fromAlbum = currentEntry && !currentEntry.hidden && currentEntry.sourceContext.kind === 'album'
    && currentEntry.sourceContext.id === currentItem?.albumId ? currentItem.album : undefined;
  const act = (action: Parameters<typeof dockCommand>[1]) => {if (page.open && page.ready && !panel && !more && commandAllowed(occupant, action)) onAction(dockCommand(occupant, action));};
  const close = () => setPanel(null);
  return <TVMusicFocus id={id} visible={page.open && !panel && !more} interactive={page.ready} entry="toggle" onClose={onClose}>
    <View testID={testID} pointerEvents={page.ready ? 'auto' : 'none'} accessibilityElementsHidden={!page.ready} importantForAccessibility={page.ready ? 'auto' : 'no-hide-descendants'} style={{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink}}>
      <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
        transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
        {p.ambient ? <View pointerEvents="none" style={{position: 'absolute', inset: 0, overflow: 'hidden'}}>
          <AmbientArt source={p.ambient} treatment="music" style={{position: 'absolute', inset: 0}}/>
        </View> : <StandardBackdrop/>}
        {fromAlbum && <TVPageGroup index={0} style={{position: 'absolute', left: 200, top: 124, flexDirection: 'row', alignItems: 'center', gap: 14}}>
          <Icon name="i-music" size={26} color={theme.colors.textSecondary}/>
          <Text style={[type(22), {color: theme.colors.textSecondary}]}>{t('next.music.playingFrom', {source: fromAlbum})}</Text>
        </TVPageGroup>}
        {p.artwork && <TVPageGroup index={0} style={{position: 'absolute', left: 200, top: 230}}><Artwork source={p.artwork} width={600} height={600} style={{borderRadius: 18,
          borderWidth: 1, borderColor: 'rgba(237,245,250,.08)', boxShadow: [{offsetX: 0, offsetY: 60, blurRadius: 120, spreadDistance: -40, color: 'rgba(0,0,0,.9)'}]}}/></TVPageGroup>}
        <ScrollView style={{position: 'absolute', left: 880, top: 280, width: 540, bottom: 96}} showsVerticalScrollIndicator={false}>
        <TVPageGroup index={0} style={{minHeight: 230}}>
          <Text accessibilityRole="header" style={[type(96, 800, -0.045), {lineHeight: 94.08 * theme.textScale, color: theme.colors.text, marginBottom: 18}]}>{p.identity.title}</Text>
          {!!artist.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16, marginBottom: 8}}>{artist.map(part => <Text key={part.id} style={[type(34, part.role === 'identity' ? 700 : 600), {color: theme.colors.textSecondary}]}>{part.text}</Text>)}</View>}
          {!!album.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>{album.map(part => <Text key={part.id} style={[type(24, part.role === 'identity' ? 700 : 400), {color: theme.colors.textTertiary}]}>{part.text}</Text>)}</View>}
        </TVPageGroup>
        <TVPageGroup index={1}>
        {signal && <MusicFocusRegion enabled={page.open && page.ready && !panel && !more} id={`${id}.signal`} entry="music-signal-line" edges={{down: `${id}.progress`, right: queueRegion}}
          style={{height: 52, marginBottom: 18, maxWidth: 500}}>
          <MusicSignalLine view={signal} focusRef={signalRef} onPress={() => setPanel('signal')}/>
        </MusicFocusRegion>}
        {!signal && <View style={{height: 70}}/>}
        <MusicFocusRegion enabled={page.open && page.ready && !panel && !more} id={`${id}.progress`} entry="seek" edges={{up: signal ? `${id}.signal` : undefined, down: `${id}.transport`, right: queueRegion}}
          style={{minHeight: 64, width: 500}}>
          <TVMusicSeek occupant={occupant} onAction={onAction} enabled={page.ready && !panel && !more} testID={`${testID}-scrubber`}/>
          <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 2}}>
            <Text style={[type(20), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, position - (bounds?.start ?? 0)))}</Text>
            <Text style={[type(20), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(span)}</Text>
          </View>
        </MusicFocusRegion>
        <MusicFocusRegion enabled={page.open && page.ready && !panel && !more} id={`${id}.transport`} entry="toggle" orientation="horizontal" edges={{up: `${id}.progress`, down: actionsRegion, right: queueRegion}}
          style={{marginTop: 28, width: 500, height: 96, flexDirection: 'row', justifyContent: policy.shuffle ? 'space-between' : 'center', alignItems: 'center', gap: policy.shuffle ? 0 : 34}}>
          {policy.shuffle}
          <Button testID={`${testID}-previous`} variant="glass" iconOnly icon="i-prev" iconSize={34} style={{height: 72, width: 72, borderRadius: 36}}
            accessibilityLabel={t('player.previous')} disabled={!p.capabilities.previous} onPress={() => act('previous')}/>
          <Button focusId="toggle" testID={`${testID}-toggle`} variant="primary" iconOnly icon={playback.playing ? 'i-pause' : 'i-play'} iconSize={44}
            style={{height: 96, width: 96, borderRadius: 48}} accessibilityLabel={t(playback.playing ? 'action.pause' : 'action.play')}
            disabled={playback.playing ? !p.capabilities.pause : !p.capabilities.play} onPress={() => act(playback.playing ? 'pause' : 'play')}/>
          <Button testID={`${testID}-next`} variant="glass" iconOnly icon="i-next" iconSize={34} style={{height: 72, width: 72, borderRadius: 36}}
            accessibilityLabel={t('player.next')} disabled={!p.capabilities.next} onPress={() => act('next')}/>
          {policy.repeat}
        </MusicFocusRegion>
        {actionsRegion && <MusicFocusRegion enabled={page.open && page.ready && !panel && !more} id={actionsRegion} entry={lyrics ? 'lyrics' : onAddToPlaylist ? 'playlist' : 'more'} orientation="horizontal" edges={{up: `${id}.transport`, right: queueRegion}}
          style={{marginTop: 38, flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>
          {lyrics && <Button ref={lyricsRef} focusId="lyrics" testID={`${testID}-lyrics`} variant="glass" size="small" icon="i-lyrics" iconSize={28} style={{height: 64, paddingHorizontal: 26}} textStyle={{fontSize: 22 * theme.textScale}}
            onPress={() => setPanel('lyrics')}>{t('player.lyrics')}</Button>}
          {onAddToPlaylist && <Button focusId="playlist" testID={`${testID}-playlist`} variant="glass" size="small" icon="i-listplus" iconSize={28}
            style={{height: 64, paddingHorizontal: 26}} textStyle={{fontSize: 22 * theme.textScale}} onPress={onAddToPlaylist}>{t('entry.addToPlaylist')}</Button>}
          {equalizer.supported && <Button ref={moreRef} focusId="more" testID={`${testID}-more`} variant="glass" iconOnly icon="i-more" iconSize={28} style={{height: 64, width: 64, borderRadius: 32}}
            accessibilityLabel={t('next.controls.more')} onPress={() => setMore(true)}/>}
        </MusicFocusRegion>}
        </TVPageGroup></ScrollView>
        {queue && <TVPageGroup index={2} style={{position: 'absolute', left: 1440, right: 96, top: 230, height: 750}}>
          <Text style={[type(32, 700, -0.019), {color: theme.colors.text, marginBottom: 6}]}>{t('next.music.upNext')}</Text>
          <NowPlayingQueue testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork}
            queueEdit={nowPlayingQueueEditPort(queue, occupant.playback.fence, queueEdit)}
            menuScope={{fenceKey: JSON.stringify(occupant.playback.fence), lease: queueEdit?.lease ?? policyLifetime}}
            interactive={page.open && page.ready && !panel && !more} focusScopeKey={nowPlayingQueueFocusScope(occupant.playback.fence)} regionId={`${id}.queue`} edges={{left: `${id}.transport`}}/>
        </TVPageGroup>}
      </View>
      <Menu testID={`${id}.more`} open={more} onOpenChange={setMore} title={t('next.controls.more')} presentation="sideSheet" returnFocusRef={moreRef} restoreFocus={panel !== 'eq'}
        items={equalizer.supported ? [{id: 'eq', label: t('next.media.eq.title'), icon: 'i-gauge', onSelect: () => {setMore(false); setPanel('eq');}}] : []}/>
      {panel === 'signal' && signal && <MusicSignalSheet view={signal} contextTitle={p.identity.title} onClose={close} returnFocusRef={signalRef}/>}
      {panel === 'lyrics' && lyrics && <SidePanel testID={`${id}.lyrics`} open title={t('player.lyrics')} width={640} topInset={0} onOpenChange={open => {if (!open) close();}} returnFocusRef={lyricsRef}>{lyrics}</SidePanel>}
      {panel === 'eq' && <View style={{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink}}>
        <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
          transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
          <StandardBackdrop/>
          <NativeWatchEqualizer focusScope={`${id}.eq`} onBack={closeEqualizer}
            copy={equalizerCopy} reportError={reportError} store={nativeEqualizer}/>
        </View>
      </View>}
      {notice && <NativeListeningNotice notice={notice} persistent/>}
    </View>
  </TVMusicFocus>;
}
