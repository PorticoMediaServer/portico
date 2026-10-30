import {useEffect, useMemo, useRef, useState, type ComponentType} from 'react';
import {defaultI18n, formatMessage} from '@i18n';
import {playbackIdentity, type PlaybackSubject} from '@core/presentation/kit-model/index.ts';
import {PlaybackDock, PlaybackPolicy, type DockPlayback, type QueuePlaybackFence, type ChannelPlaybackFence, type RemoteDock} from '../../../../packages/player-core/src/index.ts';
import {cardsFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/cards-fixtures.ts';
import {dockFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/dock-fixtures.ts';
import {createArtworkRuntime, type AmbientArtSource, type ArtworkSource} from '../../kit/artwork';
import {Button} from '../../kit/controls/Button';
import {DockHost, DockNotice, LevelMeter, MediaSurface, dockText, type DockAction, type DockBinding, type DockControl, type DockLayout, type DockMessageId, type DockNoticeData, type DockPresentation} from '../../kit/dock';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import styles from '../../kit/dock/gallery.module.css';
import {dockReferenceColors} from '../../kit/dock/dev-colors';
import afterHours from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/after-hours.jpg?url';
import tlou from '../../../../../../design-ui-v1/design/ui-v1/assets/bd-tlou.jpg?url';
import slow from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/tv/slow-horses--bd.jpg?url';
import martian from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/books/the-martian-unabridged.jpg?url';
import damn from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/damn.jpg?url';
import insideOut from '../../../../../../design-ui-v1/design/ui-v1/assets/bd-insideout.jpg?url';

const f = (id: keyof typeof dockFixtureMessages) => formatMessage(dockFixtureMessages[id]);
const scope = {serverId: 'private-design', authority: 'local' as const, accountId: 'reference', profileId: 'reference', controllerId: 'gallery', controllerEpoch: 'launch', commandLaneId: 'dock'};
type FixtureKind = DockPlayback['kind'];
type Variant = 'default' | 'paused' | 'finished' | 'ask' | 'auto' | 'next' | 'long' | 'notice-audio' | 'notice-video' | 'restored' | 'film';
type Options = {kind?: FixtureKind; layout?: DockLayout; variant?: Variant; remote?: RemoteDock['phase']; remoteKind?: RemoteDock['kind']; expanded?: boolean; web?: boolean; reduced?: boolean; morph?: boolean};
type DockEvidence = {videoSource?: string; artwork?: ArtworkSource; ambient?: AmbientArtSource};
declare global { var __porticoDockEvidence: DockEvidence | undefined; }
function useEvidence() {
  const [evidence, setEvidence] = useState(globalThis.__porticoDockEvidence);
  useEffect(() => { const change = () => setEvidence(globalThis.__porticoDockEvidence); window.addEventListener('portico-dock-evidence', change); return () => window.removeEventListener('portico-dock-evidence', change); }, []);
  return evidence;
}
function useArt() {
  const runtime = useMemo(() => createArtworkRuntime({scopeKey: 'private-dock-reference', load: async ({url}, signal) => {
    const response = await fetch(url, {signal, credentials: 'omit', redirect: 'error'}); if (!response.ok) throw new Error('Reference artwork unavailable.');
    const blob = await response.blob(), src = URL.createObjectURL(blob), image = new Image(); image.src = src;
    try { await image.decode(); if (signal.aborted) throw new DOMException('Aborted', 'AbortError'); }
    catch (error) { URL.revokeObjectURL(src); throw error; }
    return {src, bytes: blob.size, width: image.naturalWidth, height: image.naturalHeight};
  }, classify: () => ({retryable: false}), release: image => URL.revokeObjectURL(image.src)}), []);
  useEffect(() => () => runtime.dispose(), [runtime]);
  return useMemo(() => ({music: runtime.source({url: afterHours, cacheKey: afterHours}), video: runtime.source({url: tlou, cacheKey: tlou}), phoneVideo: runtime.source({url: slow, cacheKey: slow}), audiobook: runtime.source({url: martian, cacheKey: martian}), tv: runtime.source({url: damn, cacheKey: damn}), film: runtime.source({url: insideOut, cacheKey: insideOut})}), [runtime]);
}
function fence(kind: Exclude<FixtureKind, 'live'>, run = 1): QueuePlaybackFence { return {kind: 'queue', playback: {scope, run}, sessionId: `reference-${kind}-${run}`, itemId: `reference-${kind}`, sourceId: `reference-${kind}`, entryId: `reference-${kind}`, generation: 1}; }
function liveFence(run: number): ChannelPlaybackFence { return {kind: 'channel', channelKind: 'live-source', playback: {scope, run}, sessionId: `reference-live-${run}`, sourceId: 'reference-live-source', channelId: 'reference-live-channel', channelGeneration: 'reference-guide-1', generation: 1}; }
function subject(kind: FixtureKind, options: Options): PlaybackSubject {
  if (kind === 'live') return {kind: 'channel', id: 'reference-live-channel', sourceId: 'reference-live-source', channelName: formatMessage(cardsFixtureMessages['next.cards.fixture.channel']), programmeTitle: formatMessage(cardsFixtureMessages['next.cards.fixture.programme']), live: true};
  if (kind === 'music') return {kind, item: {id: 'reference-music', kind: 'song', title: f(options.layout === 'tv' ? 'next.dock.fixture.dna' : options.layout === 'phone' ? 'next.dock.fixture.tooLate' : 'next.dock.fixture.song'), artist: f(options.layout === 'tv' ? 'next.dock.fixture.kendrick' : 'next.dock.fixture.artist'), album: f('next.dock.fixture.album')}};
  if (kind === 'audiobook') return {kind, item: {id: 'reference-audiobook', kind: 'book', title: f('next.dock.fixture.martian'), author: f('next.dock.fixture.weir'), chapterNumber: 15}};
  if ((options.variant === 'film' || options.variant === 'notice-audio' && options.layout !== 'phone')) return {kind, item: {id: 'reference-video', kind: 'movie', title: f('next.dock.fixture.insideOut'), year: 2015}};
  return {kind, item: {id: 'reference-video', kind: 'episode', title: f(options.variant === 'long' ? 'next.dock.fixture.longEpisode' : options.layout === 'phone' ? 'next.dock.fixture.failure' : options.variant === 'next' ? 'next.dock.fixture.longTime' : 'next.dock.fixture.infected'), show: {title: f(options.variant === 'long' ? 'next.dock.fixture.longShow' : options.layout === 'phone' ? 'next.dock.fixture.slow' : 'next.dock.fixture.show')}, episode: {mode: 'seasonal', season: 1, episode: options.layout === 'phone' ? 1 : options.variant === 'next' ? 3 : options.variant === 'long' ? 12 : 2}}};
}
const audioControls: readonly DockControl[] = ['play', 'pause', 'replay', 'previous', 'next', 'seek', 'play-on', 'volume'];
function makePlayback(kind: FixtureKind, options: Options, run = 1): DockPlayback {
  const state = {positionSeconds: kind === 'live' ? 950 : (options.variant === 'film' || options.variant === 'notice-audio' && options.layout !== 'phone') ? 511 : options.variant === 'finished' ? 3186 : options.variant === 'next' ? 4 : options.variant === 'ask' || options.variant === 'auto' ? 605 : kind === 'music' ? options.layout === 'tv' ? options.expanded ? 96 : 88.8 : options.variant === 'notice-video' || options.layout === 'phone' ? 4 : 107 : kind === 'audiobook' && options.layout === 'tv' ? 1102 : options.layout === 'phone' || kind === 'audiobook' ? 724 : 845, playing: options.variant !== 'paused' && options.variant !== 'finished' && options.variant !== 'restored', expanded: !!options.morph};
  return kind === 'live' ? {...state, kind, fence: liveFence(run), queue: null} : {...state, kind, fence: fence(kind, run), queue: null};
}
function galleryCase(options: Options): ComponentType {
  return function DockCase() {
    const {kind = 'video', layout = 'desktop', variant = 'default'} = options, refs = useArt(), evidence = useEvidence(), stage = useRef<HTMLDivElement | null>(null), video = useRef<HTMLVideoElement | null>(null), slot = useRef<HTMLSpanElement | null>(null), full = useRef<HTMLDivElement | null>(null);
    const dock = useMemo(() => {
      const model = new PlaybackDock(scope), active = makePlayback(kind, options);
      if (variant === 'notice-audio' || variant === 'restored') model.start(makePlayback('music', {layout}, 2));
      if (variant === 'notice-video') model.start(makePlayback('video', {layout}, 2));
      model.start(active);
      if (variant === 'restored') { model.start(makePlayback('video', {}, 3)); model.close(fence('video', 3)); }
      if (options.remote) model.beginRemote({id: 'reference-remote', kind: options.remoteKind ?? 'cast', phase: options.remote, sequence: '1', playback: active, positionSeconds: layout === 'phone' ? 1420 : options.remote === 'stopped' ? 1872 : 845}, 0);
      if (active.fence.kind === 'queue' && (variant === 'ask' || variant === 'auto')) {
        const policy = new PlaybackPolicy(); policy.begin(active.fence, [{id: 'reference-intro', kind: 'intro', startSeconds: 581, endSeconds: 641, automaticSafe: true}], 3186);
        const update = policy.update({fence: active.fence, nowMs: 0, positionSeconds: 605, durationSeconds: 3186, rate: 1, playing: true, expanded: false, ended: false, kind: 'episode', modes: {intro: variant === 'auto' ? 'auto' : 'ask'}, postPlay: null});
        for (const command of update?.commands ?? []) if (command.kind === 'seek') model.update({...active, positionSeconds: command.positionSeconds});
      }
      return model;
    }, [kind, layout, variant]);
    const [state, setState] = useState(dock.getSnapshot()), [tvExpanded, setTVExpanded] = useState(options.expanded ?? false), [lastAction, setLastAction] = useState<DockAction | null>(null), [count, setCount] = useState(0), [controlValues, setControlValues] = useState({shuffle: false, repeat: 'all' as 'off' | 'all' | 'one', volume: .72, muted: false});
    const p = state.remote?.playback ?? state.local ?? makePlayback(kind, options);
    const mediaKind = state.local?.kind, mediaPosition = state.local?.positionSeconds, mediaPlaying = state.local?.playing;
    const mediaFence = state.local ? JSON.stringify(state.local.fence) : undefined, mediaRemote = state.remote?.id;
    useEffect(() => {
      const element = video.current;
      if (!element || !evidence?.videoSource || !mediaFence) return;
      const sync = () => {
        if (mediaRemote || (mediaKind !== 'video' && mediaKind !== 'live')) { element.pause(); return; }
        if (Number.isFinite(element.duration) && mediaPosition !== undefined) element.currentTime = Math.min(element.duration, mediaPosition);
        if (mediaPlaying) void element.play().catch(() => {}); else element.pause();
      };
      if (element.readyState) sync(); else element.addEventListener('loadedmetadata', sync, {once: true});
      return () => element.removeEventListener('loadedmetadata', sync);
    }, [evidence?.videoSource, mediaFence, mediaKind, mediaPosition, mediaPlaying, mediaRemote]);
    const activeKind = p.kind;
    const identity = playbackIdentity(subject(activeKind, options), 'dock', {t: defaultI18n.t});
    const presentation: DockPresentation = {fence: p.fence, identity, artwork: evidence?.artwork ?? (activeKind === 'live' ? undefined : (activeKind === 'video' && (variant === 'film' || variant === 'notice-audio' && layout !== 'phone')) ? refs.film : layout === 'tv' && activeKind === 'music' ? refs.tv : layout === 'phone' && activeKind === 'video' ? refs.phoneVideo : refs[activeKind]), ambient: evidence?.ambient,
      palette: activeKind === 'live' ? undefined : {first: activeKind === 'video' && (variant === 'film' || variant === 'notice-audio' && layout !== 'phone') ? dockReferenceColors.film : dockReferenceColors[activeKind], second: dockReferenceColors.ink},
      durationSeconds: activeKind === 'live' ? undefined : (activeKind === 'video' && (variant === 'film' || variant === 'notice-audio' && layout !== 'phone')) ? 5713 : variant === 'next' ? 4580 : activeKind === 'music' ? layout === 'tv' ? 185 : layout === 'phone' ? 239 : 211 : activeKind === 'audiobook' ? 1900 : layout === 'phone' ? 2940 : 3186,
      seekWindow: activeKind === 'live' ? {startSeconds: 900, endSeconds: 1000} : undefined,
      skipBackSeconds: activeKind === 'audiobook' ? 15 : 10, skipForwardSeconds: 30, finished: variant === 'finished', ...controlValues, rate: 1.25, sleepLabel: f('next.dock.fixture.sleep'), deviceName: f('next.dock.fixture.device'),
      album: layout === 'tv' && activeKind === 'music' ? [{id: 'album', text: f('next.dock.fixture.damn'), role: 'context'}, {id: 'year', text: f('next.dock.fixture.year'), role: 'supplement'}] : undefined,
      controls: [...audioControls.filter(control => !(activeKind === 'video' && (variant === 'film' || variant === 'notice-audio' && layout !== 'phone')) || (control !== 'previous' && control !== 'next')), ...(activeKind === 'music' ? ['shuffle', 'repeat', 'lyrics', 'queue'] as const : activeKind === 'audiobook' ? ['speed', 'sleep', 'chapters'] as const : ['subtitles-audio', 'options'] as const)]};
    const binding: DockBinding = {getSnapshot: dock.getSnapshot, onAction: action => {
      setLastAction(action); setCount(value => value + 1); window.dispatchEvent(new CustomEvent('portico-dock-action', {detail: action}));
      const local = dock.getSnapshot().local;
      if (action.kind === 'stop') { if (action.remote) dock.remote({...dock.getSnapshot().remote!, phase: 'stopped', sequence: String(BigInt(action.remote.sequence) + 1n)}, 1); else dock.close(action.fence); video.current?.pause(); }
      else if (action.kind === 'expand') dock.expand(action.fence);
      else if (action.kind === 'collapse') dock.collapse(action.fence);
      else if (action.kind === 'dismiss' && action.remote) dock.dismissRemote(action.remote.id);
      else if (action.kind === 'continue-here' && action.remote) dock.continueHere(action.remote.id);
      else if (action.kind === 'restore-audio') dock.restoreAudio();
      else if (action.kind === 'restore-video') dock.restoreVideo();
      else if (local && (action.kind === 'play' || action.kind === 'pause' || action.kind === 'replay' || action.kind === 'seek')) {
        const seconds = action.kind === 'replay' ? 0 : action.kind === 'seek' ? action.seconds! : video.current && evidence?.videoSource && (local.kind === 'video' || local.kind === 'live') ? video.current.currentTime : local.positionSeconds;
        dock.update({...local, playing: action.kind === 'play' || action.kind === 'replay' ? true : action.kind === 'pause' ? false : local.playing, positionSeconds: seconds});
        if (video.current && evidence?.videoSource) { if (action.kind === 'seek' || action.kind === 'replay') video.current.currentTime = seconds; if (action.kind === 'pause') video.current.pause(); if (action.kind === 'play' || action.kind === 'replay') void video.current.play(); }
      } else if (action.kind === 'volume' && action.volume !== undefined) { setControlValues(value => ({...value, volume: action.volume!, muted: false})); if (video.current) video.current.volume = action.volume; }
      else if (action.kind === 'mute') { setControlValues(value => ({...value, muted: !value.muted})); if (video.current) video.current.muted = !video.current.muted; }
      else if (action.kind === 'shuffle') setControlValues(value => ({...value, shuffle: !!action.shuffle}));
      else if (action.kind === 'repeat' && action.repeat) setControlValues(value => ({...value, repeat: action.repeat!}));
      setState(dock.getSnapshot());
    }};
    const bookmark = state.parkedAudio ?? state.resumeVideo;
    const notice: DockNoticeData | null = bookmark && (variant === 'notice-audio' || variant === 'notice-video') ? {kind: state.parkedAudio ? 'paused-audio' : 'stopped-video', playback: bookmark, presentation: {...presentation, fence: bookmark.fence, identity: playbackIdentity(subject(bookmark.kind, {layout}), 'dock', {t: defaultI18n.t}), artwork: state.parkedAudio ? refs.music : refs.video}, changedAtMs: 0} : null;
    return <div ref={stage} tabIndex={layout === 'tv' ? -1 : undefined} className={[styles.stage, layout === 'phone' && styles.phone, layout === 'tv' && styles.tv, options.morph && styles.morph].filter(Boolean).join(' ')} data-reduce-motion={options.reduced || undefined} data-last-action={lastAction?.kind} data-action-count={count} data-real-media={!!evidence?.videoSource} data-local-kind={state.local?.kind} data-local-playing={state.local?.playing} data-local-position={state.local?.positionSeconds} data-local-expanded={state.local?.expanded}>
      {<><div ref={full} className={styles.expandedSlot} /><MediaSurface finished={variant === 'finished'} expanded={!!state.local?.expanded} hidden={!state.local || !!state.remote || (state.local.kind !== 'video' && state.local.kind !== 'live')} dockSlot={slot} expandedSlot={full}><video ref={video} playsInline preload="metadata" src={evidence?.videoSource} /></MediaSurface>{options.morph && <div className={styles.morphTools}><Button size="small" onClick={() => binding.onAction({kind: state.local?.expanded ? 'collapse' : 'expand', fence: p.fence})}>{dockText(state.local?.expanded ? 'next.dock.fold' : 'next.dock.expand')}</Button></div>}</>}
      <DockHost state={state} presentations={[presentation]} binding={binding} layout={layout} tvReturnFocusRef={stage} tvExpanded={tvExpanded} onTVExpandedChange={setTVExpanded} artSlotRef={slot} videoSurface className={layout === 'tv' ? styles.tvSpecimen : undefined} style={layout === 'phone' ? {bottom: options.web ? 64 : options.morph || variant.startsWith('notice-') ? 92 : 0} : undefined} />
      <DockNotice notice={notice} binding={binding} nowMs={500} phone={layout === 'phone'} />
      <output className={styles.result} data-remote-sequence={state.remote?.sequence} />
    </div>;
  };
}
const cases: GalleryCase[] = [];
function add(id: string, labelKey: GalleryCase['labelKey'], options: Options, width = options.layout === 'phone' ? 390 : options.layout === 'tv' ? options.expanded ? 700 : 440 : 1440, height = options.layout === 'phone' ? options.morph ? 844 : 62 : options.layout === 'tv' ? options.expanded ? 470 : 84 : options.morph ? 900 : options.variant?.startsWith('notice-') ? 220 : 92) { cases.push({id, labelKey, width, height, render: galleryCase(options)}); }
add('desktop-video', 'next.dock.case.video', {});
add('desktop-live', 'next.kit.live', {kind: 'live'});
add('desktop-video-hover', 'next.dock.case.hover', {}, 1440, 132);
add('desktop-intro-ask', 'next.dock.case.ask', {variant: 'ask'});
add('desktop-intro-auto', 'next.dock.case.automatic', {variant: 'auto'});
add('desktop-next', 'next.dock.case.next', {variant: 'next'}, 1360);
add('desktop-finished', 'next.dock.case.finished', {variant: 'finished'}, 1360);
add('desktop-music', 'next.dock.case.music', {kind: 'music'});
add('desktop-music-paused', 'next.dock.case.paused', {kind: 'music', variant: 'paused'});
add('desktop-audiobook', 'next.dock.case.audiobook', {kind: 'audiobook'});
add('desktop-restored', 'next.dock.case.paused', {kind: 'music', variant: 'restored'});
add('desktop-video-paused-music', 'next.dock.case.handover', {variant: 'notice-audio'});
add('desktop-music-resume-video', 'next.dock.case.handover', {kind: 'music', variant: 'notice-video'});
add('desktop-long', 'next.dock.case.long', {variant: 'long'});
add('desktop-film', 'next.dock.case.video', {variant: 'film'});
for (const remoteKind of ['cast', 'play-on'] as const) for (const phase of ['connecting', 'pairing', 'waiting', 'playing', 'stopped', 'replaced', 'error'] as const) {
  const labelKey = phase === 'pairing' ? 'next.dock.case.connecting' : `next.dock.case.${phase}` as DockMessageId;
  add(`desktop-${remoteKind}-${phase}`, labelKey, {remote: phase, remoteKind}, 1360);
  add(`phone-${remoteKind}-${phase}`, labelKey, {layout: 'phone', remote: phase, remoteKind});
}
add('phone-video', 'next.dock.case.video', {layout: 'phone'});
add('phone-live', 'next.kit.live', {layout: 'phone', kind: 'live'});
add('phone-video-menu', 'next.dock.case.menu', {layout: 'phone'}, 390, 300);
add('phone-music', 'next.dock.case.music', {layout: 'phone', kind: 'music'});
add('phone-audiobook', 'next.dock.case.audiobook', {layout: 'phone', kind: 'audiobook'});
add('phone-restored', 'next.dock.case.paused', {layout: 'phone', kind: 'music', variant: 'restored'});
add('phone-finished', 'next.dock.case.finished', {layout: 'phone', variant: 'finished'});
add('phone-long', 'next.dock.case.long', {layout: 'phone', variant: 'long'});
add('phone-video-paused-music', 'next.dock.case.handover', {layout: 'phone', variant: 'notice-audio'}, 390, 844);
add('phone-music-resume-video', 'next.dock.case.handover', {layout: 'phone', kind: 'music', variant: 'notice-video'}, 390, 844);
add('phone-video-web', 'next.dock.case.video', {layout: 'phone', web: true}, 390, 844);
add('phone-music-web', 'next.dock.case.music', {layout: 'phone', kind: 'music', web: true}, 390, 844);
add('tv-music', 'next.dock.case.tv', {layout: 'tv', kind: 'music'});
add('tv-audiobook-paused', 'next.dock.case.paused', {layout: 'tv', kind: 'audiobook', variant: 'paused'});
add('tv-expanded', 'next.dock.case.tvExpanded', {layout: 'tv', kind: 'music', expanded: true});
add('tv-expanded-paused', 'next.dock.case.paused', {layout: 'tv', kind: 'music', expanded: true, variant: 'paused'});
add('tv-expanded-audiobook', 'next.dock.case.tvExpanded', {layout: 'tv', kind: 'audiobook', expanded: true});
add('morph-desktop', 'next.dock.case.morph', {morph: true});
add('morph-phone', 'next.dock.case.morph', {morph: true, layout: 'phone'}, 390, 844);
add('morph-reduced', 'next.dock.case.reduced', {morph: true, reduced: true});
cases.push({id: 'level-meter-web', labelKey: 'next.dock.case.meter', width: 18, height: 14, render: () => <LevelMeter />}, {id: 'level-meter-tv', labelKey: 'next.dock.case.meter', width: 23, height: 20, render: () => <LevelMeter variant="tv-large" />});
export default defineGalleryFamily({id: 'dock', labelKey: 'next.dock.family', cases});
