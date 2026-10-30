import React, {useCallback, useMemo, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {PlaybackDock, type DockPlayback, type DockState, type QueuePlaybackFence, type QueueScope, type RemotePhase} from '../../../../../../packages/player-core/src/index';
import {playbackIdentity} from '../../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {dockNativeFixtures as facts} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/dock-native-fixtures';
import {DockHost, MediaBar, PlayerMorph, usePlayerMorph, type DockCommand, type DockPresentation, type DockRect} from '../../../kit/dock';
import {currentOccupant} from '../../../kit/dock/model';
import {Button} from '../../../kit/controls';
import {OverlaysProvider} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import type {ArtworkSource} from '../../../kit/artwork';
import type {Gallery} from '../../gallery-registry';

type Evidence = {
  dock: PlaybackDock;
  getSnapshot: () => DockState;
  subscribe: (listener: () => void) => () => void;
  presentation: {local?: DockPresentation; remote?: DockPresentation};
  onAction: (command: DockCommand) => void;
  /** Actual runtime surface rendered once by the evidence owner, never replaced with a poster. */
  surface?: React.ComponentProps<typeof PlayerMorph>['children'];
  retainedSnapshot?: React.ComponentProps<typeof PlayerMorph>['retainedSnapshot'];
  notice?: React.ComponentProps<typeof DockHost>['notice'];
  onNoticeDismiss?: React.ComponentProps<typeof DockHost>['onNoticeDismiss'];
  artwork?: Readonly<{video?: ArtworkSource; music?: ArtworkSource; tvMusic?: ArtworkSource; book?: ArtworkSource}>;
};
declare global {var __porticoNativeDockEvidence: Evidence | undefined; var __porticoNativeDockCommands: DockCommand[] | undefined;}
const scope: QueueScope = {serverId: 'gallery-server', authority: 'local', accountId: 'gallery-account', profileId: 'gallery-profile', controllerId: 'gallery-controller', controllerEpoch: 'gallery-epoch', commandLaneId: 'gallery-lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'gallery-session', itemId: 'gallery-item', sourceId: 'gallery-source', entryId: 'gallery-entry', generation: 1};
type Case = 'music' | 'audiobook' | 'video' | 'menu' | 'finished' | 'paused' | 'handover' | 'connecting' | 'waiting' | 'playing' | 'play-on' | 'stopped' | 'replaced' | 'error' | 'tv-music' | 'tv-book' | 'tv-expanded' | 'gesture';

/** Deterministic DEV-only arbiter inputs. This does not assert real engine or session execution. */
function fixture(kind: Case) {
  const book = kind === 'audiobook' || kind === 'tv-book', tv = kind.startsWith('tv-');
  const remotePhase: RemotePhase | undefined = kind === 'play-on' ? 'playing' : kind === 'connecting' || kind === 'waiting' || kind === 'playing' || kind === 'stopped' || kind === 'replaced' || kind === 'error' ? kind : undefined;
  const remote = remotePhase !== undefined;
  const video = remote || ['video', 'menu', 'finished', 'gesture', 'handover'].includes(kind);
  const dock = new PlaybackDock(scope);
  const item = book ? {id: fence.itemId, kind: 'audiobook_file' as const, title: facts.book.title, author: facts.book.author, chapterNumber: 15} : video ? {id: fence.itemId, kind: 'episode' as const, title: facts.video.episode, show: {title: facts.video.title}, episode: {mode: 'seasonal' as const, season: 1, episode: 1}} : {id: fence.itemId, kind: 'song' as const, title: tv ? facts.tvMusic.title : facts.music.title, artist: tv ? facts.tvMusic.artist : facts.music.artist};
  const mode = book ? 'audiobook' : video ? 'video' : 'music';
  const local: DockPlayback = {fence, kind: mode, queue: null, positionSeconds: book ? 20880 : kind === 'finished' ? 2940 : video ? 724 : tv ? 96 : 9.56, playing: !['paused', 'tv-book', 'finished'].includes(kind), expanded: false};
  if (kind === 'handover') dock.start({...local, kind: 'music', positionSeconds: 34, fence: {...fence, sessionId: 'gallery-audio', sourceId: 'gallery-audio', itemId: 'gallery-song'}});
  dock.start(local);
  if (remotePhase) dock.beginRemote({id: 'gallery-remote', kind: kind === 'play-on' ? 'play-on' : 'cast', phase: remotePhase, sequence: '1', playback: local, positionSeconds: 1420}, 0);
  const model: DockPresentation = {fence, identity: playbackIdentity({kind: mode, item}, 'dock', {t: defaultI18n.t}), durationSeconds: book ? 36000 : video ? 2940 : kind === 'tv-music' ? 200 : tv ? 185 : 239, skipBackSeconds: 10,
    capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false}, finished: kind === 'finished',
    ...(tv && !book ? {album: [{id: 'album', text: facts.tvMusic.album, role: 'context'}, {id: 'year', text: facts.tvMusic.year, role: 'context'}] as const} : {}), ...(remote ? {device: facts.device} : {})};
  return {dock, model};
}

function DockCase({kind}: {kind: Case}) {
  const theme = useTheme();
  const injected = globalThis.__porticoNativeDockEvidence;
  const prepared = useMemo(() => fixture(kind), [kind]);
  const [snapshot, setSnapshot] = useState(prepared.dock.getSnapshot()), [expanded, setExpanded] = useState(kind === 'tv-expanded'), [menu, setMenu] = useState(kind === 'menu'), [noticeDismissed, setNoticeDismissed] = useState(false);
  const [slot, setSlot] = useState<DockRect>({x: 15, y: 698.5, width: 80, height: 45});
  const [docked, setDocked] = useState(true);
  const external = useSyncExternalStore(injected?.subscribe ?? (() => () => {}), injected?.getSnapshot ?? (() => snapshot));
  const dock = injected?.dock ?? prepared.dock;
  const artwork = injected?.artwork?.[kind === 'tv-book' || kind === 'audiobook' ? 'book' : kind.startsWith('tv-') ? 'tvMusic' : ['video', 'menu', 'finished', 'gesture'].includes(kind) ? 'video' : 'music'];
  const restoredMusic = kind === 'handover' && snapshot.local?.kind === 'music' ? snapshot.local : undefined;
  const localModel = {...prepared.model, fence: snapshot.local?.fence ?? prepared.model.fence, artwork: restoredMusic ? injected?.artwork?.music : artwork,
    ...(restoredMusic ? {identity: playbackIdentity({kind: 'music', item: {id: restoredMusic.fence.itemId, kind: 'song', title: facts.music.title, artist: facts.music.artist}}, 'dock', {t: defaultI18n.t}), durationSeconds: 239} : {}),
    finished: !!prepared.model.finished && snapshot.local?.positionSeconds === 2940 && !snapshot.local.playing};
  const presentation = injected?.presentation ?? {local: localModel, remote: {...prepared.model, fence: snapshot.remote?.playback.fence ?? prepared.model.fence, artwork}};
  const action = useCallback((command: DockCommand) => {
    if (injected) {injected.onAction(command); return;}
    if(command.fence.kind==='cast-view')return; // View-only custody cannot enter fixture player-core transitions.
    const state = dock.getSnapshot();
    const commands = globalThis.__porticoNativeDockCommands ??= [];
    commands.push(command);
    if (commands.length > 128) commands.splice(0, commands.length - 128);
    // These controlled fixture transitions exercise callbacks only. Real-media proof requires injection.
    if (command.remote) {
      const remote = state.remote;
      if (!remote) return;
      if (command.type === 'dismiss-remote' || command.type === 'stop') dock.dismissRemote(remote.id);
      else if (command.type === 'continue-here') dock.continueHere(remote.id);
      else if (command.type === 'seek') dock.remote({...remote, positionSeconds: command.positionSeconds ?? remote.positionSeconds, sequence: String(BigInt(remote.sequence) + 1n)}, 1);
      else if (command.type === 'pause' || command.type === 'play') dock.remote({...remote, playback: {...remote.playback, playing: command.type === 'play'}, sequence: String(BigInt(remote.sequence) + 1n)}, 1);
    } else if (command.type === 'expand') {setDocked(false); dock.expand(command.fence);}
    else if (command.type === 'collapse') {setDocked(true); dock.collapse(command.fence);}
    else if (command.type === 'close' || command.type === 'stop') dock.close(command.fence);
    else if (command.type === 'restore-video') dock.restoreVideo();
    else if (command.type === 'restore-audio') dock.restoreAudio();
    else if (state.local) {
      const p = state.local;
      if (command.type === 'play' || command.type === 'pause') dock.update({...p, playing: command.type === 'play'});
      else if (command.type === 'replay') dock.update({...p, playing: true, positionSeconds: 0});
      else if (command.type === 'seek') dock.update({...p, positionSeconds: command.positionSeconds ?? p.positionSeconds});
    }
    setSnapshot(dock.getSnapshot());
  }, [injected, dock]);
  const localFence = presentation.local?.fence;
  const morph = usePlayerMorph({docked, fence: localFence, travel: 690, onRelease: value => {if (localFence) action({type: value ? 'collapse' : 'expand', fence: localFence});}});
  const occupant = currentOccupant(dock, external, presentation);
  const parked = !injected && snapshot.parkedAudio?.kind !== 'live' ? snapshot.parkedAudio : null;
  const notice = injected ? injected.notice : !noticeDismissed && parked ? {id: 'gallery-paused-audio', kind: 'paused-audio' as const, fence: parked.fence, identity: playbackIdentity({kind: 'music', item: {id: parked.fence.itemId, kind: 'song', title: facts.music.title, artist: facts.music.artist}}, 'dock', {t: defaultI18n.t}), positionSeconds: parked.positionSeconds} : undefined;
  return <OverlaysProvider><View style={{width: theme.isTV ? 1920 : 390, height: theme.isTV ? 1080 : 844, backgroundColor: theme.colors.ink}}>
    {kind === 'menu' && occupant ? <MediaBar occupant={occupant} onAction={action} menuOpen={menu} onMenuOpenChange={setMenu} style={{position: 'absolute', left: 8, right: 8, bottom: 92}}/> : <DockHost testID={`dev.dock.${kind}`} dock={dock} snapshot={external} presentation={presentation} onAction={action} tvExpanded={expanded} onTVExpandedChange={setExpanded} morph={kind === 'gesture' ? morph : undefined} onVideoSlot={setSlot} bottomInset={92} notice={notice} onNoticeDismiss={injected ? injected.onNoticeDismiss : () => setNoticeDismissed(true)}/>}
    {injected?.surface && <PlayerMorph controller={morph} fence={localFence} docked={docked} fullFrame={{x: 0, y: 0, width: 390, height: 219.375}} dockFrame={slot} retainedSnapshot={injected.retainedSnapshot}>{injected.surface}</PlayerMorph>}
    {!docked && localFence && <Button variant="text" onPress={() => action({type: 'collapse', fence: localFence})}>{defaultI18n.t('player.minimize')}</Button>}
  </View></OverlaysProvider>;
}

const cases: readonly [string, Case, 'phone' | 'tv'][] = [
  ['dock-phone-music', 'music', 'phone'], ['dock-phone-audiobook', 'audiobook', 'phone'], ['dock-phone-video', 'video', 'phone'], ['dock-phone-menu', 'menu', 'phone'], ['dock-phone-finished', 'finished', 'phone'], ['dock-phone-paused', 'paused', 'phone'],
  ['dock-phone-handover', 'handover', 'phone'],
  ['dock-phone-remote-connecting', 'connecting', 'phone'], ['dock-phone-remote-waiting', 'waiting', 'phone'], ['dock-phone-remote-playing', 'playing', 'phone'], ['dock-phone-remote-stopped', 'stopped', 'phone'], ['dock-phone-remote-replaced', 'replaced', 'phone'], ['dock-phone-remote-error', 'error', 'phone'],
  ['dock-phone-play-on', 'play-on', 'phone'],
  ['dock-tv-music', 'tv-music', 'tv'], ['dock-tv-audiobook', 'tv-book', 'tv'], ['dock-tv-expanded', 'tv-expanded', 'tv'], ['dock-phone-gesture', 'gesture', 'phone'],
];
const galleries: Gallery[] = cases.map(([id, kind, platform]) => ({id, title: platform === 'tv' ? 'next.dock.native.gallery.tv' : kind === 'gesture' ? 'next.dock.native.gallery.motion' : kind === 'music' || kind === 'audiobook' || kind === 'paused' ? 'next.dock.native.gallery.music' : kind.startsWith('tv-') ? 'next.dock.native.gallery.tv' : ['video', 'menu', 'finished'].includes(kind) ? 'next.dock.native.gallery.video' : 'next.dock.native.gallery.remote', platforms: platform === 'tv' ? ['tv'] : ['phone', 'tablet'], render: function GalleryCase() {return <DockCase kind={kind}/>;}}));
export default galleries;
