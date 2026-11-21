import {useCallback, useEffect, useId, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import {sameFence} from '../../../../../../packages/player-core/src';
import {provideTVLayerClaim, requestNativeFocus} from '../../../kit/focus';
import {commandAllowed, dockCommand, playbackPosition, seekBounds} from '../../../kit/dock/model';
import {shellLayout} from '../../../shell/model';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {registerOverlayKeys} from '../../../platform/overlay-keys';
import type {NativeEqualizerCopy} from '../../../kit/frames/EqualizerFrame';
import {listeningSongFavorite} from '../../../screens/destinations/music/ListeningSongActions';
import {nowPlayingQueueRows, nowPlayingSource} from '../../../screens/destinations/music/NowPlayingQueue';
import type {NowPlayingScreenProps} from './audio-player';
import {useTVQueuePolicy} from '../../../screens/destinations/music/TVQueuePolicy';
import {useTVPageMotion} from '../../../tv/layout/TVPageMotion';
import {registerBack} from '../../../kit/navigation/back';

export type TVAudioPlayerProps = NowPlayingScreenProps & {equalizerCopy: NativeEqualizerCopy};

export function useTVAudioPlayer({occupant, queue, width, height, onAction, onAddToPlaylist, queuePolicy, song, signal,
  reportError = () => {}, testID = 'now-playing', lyrics}: TVAudioPlayerProps) {
  const id = useId(), canvas = shellLayout(width, height, true);
  const page = useTVPageMotion();
  const [panel, setPanel] = useState<'signal' | 'eq' | 'lyrics' | null>(null), [more, setMore] = useState(false);
  const signalRef = useRef<View>(null), moreRef = useRef<View>(null), lyricsRef = useRef<View>(null);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const policy = useTVQueuePolicy({fence: occupant.playback.fence, queue, policy: queuePolicy, reportError, testID});
  const returnFocus = useRef<{fence: typeof occupant.playback.fence; id: string} | undefined>(undefined);
  const latest = useRef({panel, fence: occupant.playback.fence});
  latest.current = {panel, fence: occupant.playback.fence};
  const close = useCallback(() => {
    if (!panel || latest.current.panel !== panel || !sameFence(latest.current.fence, occupant.playback.fence)) return false;
    returnFocus.current = {fence: occupant.playback.fence, id: panel === 'lyrics' ? 'lyrics' : panel === 'signal' ? 'music-signal-line' : 'more'};
    setPanel(null);
    return true;
  }, [panel, occupant.playback.fence]);
  const startFocus = !panel && !more && page.open && page.ready && returnFocus.current
    && sameFence(returnFocus.current.fence, occupant.playback.fence) ? returnFocus.current.id : undefined;
  useEffect(() => {
    if (panel !== 'eq') return;
    const releaseBack = registerBack(close, {scope: 'overlay'});
    const release = registerOverlayKeys(key => key === 'escape' || key === 'menu' ? close() : false);
    return () => {releaseBack(); release();};
  }, [panel, close]);
  const {presentation: p, playback} = occupant;
  const position = playbackPosition(occupant), bounds = seekBounds(occupant), span = bounds ? bounds.end - bounds.start : p.durationSeconds;
  const artist = p.identity.subtitle.filter(part => !!part.text);
  const album = (p.album ?? []).filter(part => !!part.text);
  // Right names a queue containing a real readable native focus stop.
  const queueRegion = queue && nowPlayingQueueRows(queue).some(row => !row.current) ? `${id}.queue` : undefined;
  const favourite = song && listeningSongFavorite(song.token, song.pending);
  const actionsRegion = lyrics || onAddToPlaylist || equalizer.supported || favourite ? `${id}.actions` : undefined;
  const enabled = page.open && page.ready && !panel && !more;
  const currentItem = queue?.items.find(item => item.entryId === queue.queue.currentEntryId);
  const playingFrom = nowPlayingSource(queue);
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
  return {id, canvas, page, panel, setPanel, more, setMore, signalRef, moreRef, lyricsRef, equalizer, policy,
    position, bounds, span, artist, album, queueRegion, favourite, actionsRegion, enabled, currentItem, playingFrom,
    previousRef, toggleRef, nextRef, playlistRef, act, close, startFocus};
}
