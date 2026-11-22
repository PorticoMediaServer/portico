import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {AudioPlayerFrame} from '../../../kit/frames';
import {TVAudioCanvas, TVAudioEqualizer, TVAudioLyrics, TVAudioSurface} from '../../../kit/player/TVAudioSurface';
import {Menu} from '../../../kit/overlays';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {EqualizerPage} from '../../shared/player/EqualizerPage';
import {MusicSignalLine, MusicSignalSheet} from '../../../screens/destinations/music/MusicSignal';
import {NativeListeningNotice} from '../../../kit/player/ListeningNotice';
import {NowPlayingQueue} from '../../../kit/player/NowPlayingQueue';
import {nowPlayingQueueFocusScope, nowPlayingQueueEditPort} from '../../shared/player/useNowPlayingQueue';
import {TVMusicFocus, TVMusicSeek} from '../../../kit/player/TVMusicFocus';
import {TVPageTransition} from '../../../tv/layout/TVPageMotion';
import {useTVAudioPlayer, type TVAudioPlayerProps} from '../../shared/player/useTVAudioPlayer';

export function TVAudioPlayerPage(props: TVAudioPlayerProps) {
  return <TVPageTransition open={props.tvOpen ?? true} onExited={props.onTVExited ?? (() => {})}><TVAudioPlayerContent {...props}/></TVPageTransition>;
}

function TVAudioPlayerContent(props: TVAudioPlayerProps) {
  const {occupant, queue, artwork, onAction, onQueuePlay, onAddToPlaylist, policyLifetime, queueEdit, song, onClose, signal, notice,
    reportError = () => {}, testID = 'now-playing', lyrics, equalizerCopy} = props;
  const t = defaultI18n.t, {presentation: p, playback} = occupant;
  const {id, canvas, page, panel, setPanel, more, setMore, signalRef, moreRef, lyricsRef, equalizer, policy,
    position, bounds, span, artist, album, queueRegion, favourite, actionsRegion, enabled, currentItem, playingFrom,
    previousRef, toggleRef, nextRef, playlistRef, act, close, startFocus} = useTVAudioPlayer(props);
  return <TVMusicFocus id={id} visible={page.open} covered={!!panel || more} interactive={page.ready} entry="toggle" onClose={onClose}>
    <TVAudioSurface testID={testID} ready={page.ready} ambient={p.ambient}>
      <TVAudioCanvas canvas={canvas}>
      <AudioPlayerFrame startFocus={startFocus} title={p.identity.title} icon={occupant.mode === 'audiobook' ? 'i-book' : 'i-music'}
        artwork={p.artwork ?? artwork?.(currentItem?.posterUrl)}
        context={playingFrom ? t('web.player.playingFromLabel') : t('player.queueNowPlaying')} source={playingFrom}
        byline={[artist.map(part => part.text).join(' '), album.map(part => part.text).join(' ') || currentItem?.album || '']} close={{icon: 'i-down', label: t('player.minimize'), onPress: onClose}}
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
        favourite={favourite ? {icon: favourite.favorite ? 'i-heart-f' : 'i-heart', active: favourite.favorite, disabled: favourite.pending,
          label: t(favourite.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), focusId: 'favorite', testID: `${testID}-favorite`,
          onPress: () => {if (enabled && song?.token) song.favorite(song.token, !favourite.favorite);}} : undefined}
        tvFocus={{
          signal: {id: `${id}.signal`, entry: 'music-signal-line', enabled, edges: {down: `${id}.progress`, right: queueRegion}},
          seek: {id: `${id}.progress`, entry: 'seek', enabled, edges: {up: signal ? `${id}.signal` : undefined, down: `${id}.transport`, right: queueRegion}},
          transport: {id: `${id}.transport`, entry: 'toggle', enabled, orientation: 'horizontal', edges: {up: `${id}.progress`, down: actionsRegion, right: queueRegion}},
          actions: actionsRegion ? {id: actionsRegion, entry: lyrics ? 'lyrics' : onAddToPlaylist ? 'playlist' : favourite ? 'favorite' : 'more',
            enabled, orientation: 'horizontal', edges: {up: `${id}.transport`, right: queueRegion}} : undefined,
        }}
        panelTitle={queue ? t('next.music.upNext') : undefined}
        panel={queue && <NowPlayingQueue testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork}
          queueEdit={nowPlayingQueueEditPort(queue, occupant.playback.fence, queueEdit)}
          menuScope={{fenceKey: JSON.stringify(occupant.playback.fence), lease: queueEdit?.lease ?? policyLifetime}}
          interactive={enabled} focusScopeKey={nowPlayingQueueFocusScope(occupant.playback.fence)}
          regionId={`${id}.queue`} edges={{left: `${id}.transport`}}/>}/>
      </TVAudioCanvas>
      <Menu testID={`${id}.more`} open={more} onOpenChange={setMore} title={t('next.controls.more')} presentation="sideSheet" returnFocusRef={moreRef} restoreFocus={panel !== 'eq'}
        items={equalizer.supported ? [{id: 'eq', label: t('next.media.eq.title'), icon: 'i-gauge', onSelect: () => {setMore(false); setPanel('eq');}}] : []}/>
      {panel === 'signal' && signal && <MusicSignalSheet view={signal} contextTitle={p.identity.title} onClose={close} returnFocusRef={signalRef}/>}
      {panel === 'lyrics' && lyrics && <TVAudioLyrics testID={`${id}.lyrics`} open title={t('player.lyrics')} onOpenChange={open => {if (!open) close();}} returnFocusRef={lyricsRef} restoreFocus={false}>{lyrics}</TVAudioLyrics>}
      {panel === 'eq' && <TVAudioEqualizer canvas={canvas}>
        <EqualizerPage focusScope={`${id}.eq`} onBack={close}
          copy={equalizerCopy} reportError={reportError} store={nativeEqualizer}/>
      </TVAudioEqualizer>}
      {notice && <NativeListeningNotice notice={notice} persistent/>}
    </TVAudioSurface>
  </TVMusicFocus>;
}
