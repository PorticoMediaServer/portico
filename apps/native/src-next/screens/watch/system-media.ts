import {NativeEventEmitter, NativeModules} from 'react-native';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/identity';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import {tvKeySource} from '../../platform/tv-key-source';
import {playbackIdentity, type PlaybackSubject} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {admitTVPlayPause, tvPlayPauseTogglesPlayback} from '../../platform/tv-play-pause';

/** N1-1: every field both native modules read (PorticoSystemMedia.m `validated:`,
 * PorticoSystemMedia.kt `loadArtwork`/`describe`), bounded as they are. */
type VideoMetadata = Readonly<{
  itemId: string; sourceToken: string; title: string; subtitle?: string;
  durationSeconds: number;
  live: boolean; canNext: boolean; canPrevious: boolean; skipBackSeconds: number; skipForwardSeconds: number;
  origin?: string; bearer?: string; posterPath?: string;
  chapters?: readonly Readonly<{startSeconds: number; title: string}>[];
  markers?: readonly Readonly<{kind: string; startSeconds: number; endSeconds: number}>[];
}>;
export type NativeVideoSystemMediaPort = Readonly<{
  setVideo: (metadata: VideoMetadata) => void;
  clear: () => void;
  subscribe: (listener: (event: unknown) => void) => () => void;
}>;
type Context = Pick<NativeShellBootstrapContext, 'playback' | 'current' | 'authority' | 'subscribeAuthority' | 'preferences' | 'subscribePreferences'> & Partial<Pick<NativeShellBootstrapContext, 'session' | 'api'>>;
let nativeOwner: object | undefined;
// Opaque per-binding token the native module echoes on each command.
let sourceSequence = 0;

/** One owner per restored authority; the listening engine retains its own session.
 * Vega already installs its source-bound SDK consumer and must not receive another. */
export function bindNativeVideoSystemMedia(context: Context, reportError: (error: unknown) => void): () => void {
  if (tvKeySource === 'vega') return () => {};
  const module = NativeModules.PorticoSystemMedia;
  if (typeof module?.setVideo !== 'function' || typeof module?.clear !== 'function') return () => {};
  const emitter = new NativeEventEmitter(module), owner = {}; nativeOwner = owner;
  let stop: () => void;
  try {
    stop = bindVideoSystemMedia(context, {
      setVideo: metadata => {if (nativeOwner === owner) module.setVideo(metadata);}, clear: () => {if (nativeOwner === owner) module.clear();},
      subscribe: listener => {const subscription = emitter.addListener('PorticoSystemMediaCommand', event => {if (nativeOwner === owner) listener(event);}); return () => subscription.remove();},
    }, reportError);
  } catch (error) {if (nativeOwner === owner) nativeOwner = undefined; throw error;}
  return () => {try {stop();} finally {if (nativeOwner === owner) nativeOwner = undefined;}};
}

/** notes/system-copy.md §4: the OS's Now Playing shows the dock's two lines and nothing more.
 * A film: its title over its year. An episode: the show over "S1 E1, Failure's Contagious". Live TV:
 * the programme over its channel (nothing twice). No fallback title: an empty line stays unset. */
export function nowPlayingLines(subject: PlaybackSubject | undefined, entry: ContentEntry | undefined): Readonly<{title: string; subtitle?: string}> {
  if (!subject) return {title: entry?.displayTitle ?? entry?.title ?? ''};
  const identity = playbackIdentity(subject, 'dock', {t: defaultI18n.t}), part = (id: string) => identity.subtitle.find(value => value.id === id)?.text;
  if (subject.kind === 'channel') return {title: identity.title, ...(part('channel') ? {subtitle: part('channel')} : {})};
  if (subject.item.kind === 'episode') {
    const label = identity.subtitle.find(value => value.id !== 'episode-title')?.text, own = part('episode-title');
    const subtitle = label && own ? defaultI18n.t('system.nowPlaying.episodeLine', {label, title: own}) : label ?? own;
    return {title: identity.title, ...(subtitle ? {subtitle} : {})};
  }
  const year = subject.item.year;
  return {title: identity.title, ...(typeof year === 'number' && Number.isInteger(year) && year > 0 ? {subtitle: String(year)} : {})};
}

/** The server origin the natives fetch artwork from (no credentials, query or path), and the
 * protected poster path they accept (`/v1/items/{id}/art/(poster|backdrop|thumb)`). */
export function artworkOrigin(baseUrl: string): string | undefined {
  try {const url = new URL(baseUrl); return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password && (url.pathname === '/' || url.pathname === '') ? url.origin : undefined;} catch {return undefined;}
}
export function artworkPath(entry: ContentEntry | undefined): string | undefined {
  for (const value of [entry?.stillUrl, entry?.posterUrl]) {
    const path = value?.split('?')[0];
    if (path && /^\/v1\/items\/[A-Za-z0-9_-]{1,128}\/art\/(poster|backdrop|thumb)$/.test(path)) return path;
  }
}

/** The native module only reports a request. The original adapter owns effects,
 * capabilities and Watch Together policy; no TV-event transport path competes with it. */
export function bindVideoSystemMedia(context: Context, port: NativeVideoSystemMediaPort, reportError: (error: unknown) => void): () => void {
  const playback = context.playback, authority = Object.freeze({...context.authority()});
  let disposed = false, published = false, metadataKey: string | undefined;
  let binding: Readonly<{fence: PlaybackFence; lease: string; itemId: string; sourceToken: string}> | undefined;
  let offCommands: (() => void) | undefined;
  const current = () => !disposed && context.current() && context.playback === playback && sameAuthority(context.authority(), authority);
  const source = () => {
    if (!current()) return;
    const state = playback.getSnapshot(), local = state.dock.local, fence = state.fence;
    if (state.retired || !sameAuthority(state.authority, authority) || !fence || !local || !sameFence(local.fence, fence)
      || local.kind !== 'video' && local.kind !== 'live' || state.playback.nativeListening) return;
    return state;
  };
  const clear = () => {
    offCommands?.(); offCommands = undefined; binding = undefined; metadataKey = undefined;
    if (published) {published = false; port.clear();}
  };
  const update = () => {
    const state = source();
    if (!state?.fence) {clear(); return;}
    const fence = state.fence, itemId = fence.kind === 'channel' ? fence.channelId : fence.itemId;
    if (!binding || !sameFence(binding.fence, fence) || binding.lease !== state.lease) {
      offCommands?.(); offCommands = undefined; binding = undefined;
      const captured = {fence, lease: state.lease, itemId, sourceToken: `video-${++sourceSequence}`};
      offCommands = port.subscribe(event => {
        const live = source();
        if (!live || binding !== captured || live.lease !== captured.lease || !sameFence(live.fence, captured.fence)
          || !event || typeof event !== 'object') return;
        const command = event as {command?: unknown; itemId?: unknown; sourceToken?: unknown; positionSeconds?: unknown; deltaSeconds?: unknown};
        // The native event captured this token at dispatch. Matching the title
        // alone would admit an old event into a new session for the same title.
        if (command.itemId !== captured.itemId || command.sourceToken !== captured.sourceToken) return;
        const ready = live.playback.phase !== 'ended' && live.playback.phase !== 'error';
        // Apple TV: a Siri Remote press the player already took as a key is not toggled twice.
        if ((command.command === 'play' || command.command === 'pause' || command.command === 'toggle') && tvPlayPauseTogglesPlayback() && !admitTVPlayPause('system')) return;
        let operation: Promise<void> | undefined;
        try {
          switch (command.command) {
            case 'play': if (ready && live.capabilities.play && live.playback.intent === 'paused') operation = playback.toggle(captured.fence); break;
            case 'pause': if (ready && live.capabilities.pause && live.playback.intent === 'playing') operation = playback.toggle(captured.fence); break;
            case 'toggle': if (ready && (live.playback.intent === 'playing' ? live.capabilities.pause : live.capabilities.play)) operation = playback.toggle(captured.fence); break;
            case 'seek': if (ready && live.capabilities.seek && typeof command.positionSeconds === 'number' && Number.isFinite(command.positionSeconds)) operation = playback.seek(captured.fence, command.positionSeconds); break;
            case 'skip': if (ready && live.capabilities.seek && typeof command.deltaSeconds === 'number' && Number.isFinite(command.deltaSeconds)) operation = playback.skip(captured.fence, command.deltaSeconds); break;
            case 'next': if (ready && live.capabilities.next) operation = playback.next(captured.fence); break;
            case 'previous': if (ready && live.capabilities.previous) operation = playback.previous(captured.fence); break;
          }
          void operation?.catch(error => {if (current() && binding === captured) reportError(error);});
        } catch (error) {if (current() && binding === captured) reportError(error);}
      });
      binding = captured;
    }
    const values = context.preferences().effective;
    const seconds = (key: string, fallback: number) => {
      const value = values[key];
      return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value / 1000 : fallback;
    };
    const entry = state.entry, lines = nowPlayingLines(state.subject, entry);
    const session = context.session?.(), origin = context.api ? artworkOrigin(context.api.baseUrl) : undefined, posterPath = artworkPath(entry);
    const options = state.options?.itemId === itemId ? state.options : undefined;
    const metadata: VideoMetadata = {
      itemId, sourceToken: binding.sourceToken, title: lines.title,
      ...(lines.subtitle ? {subtitle: lines.subtitle} : {}),
      ...(origin && posterPath && session?.accessToken ? {origin, bearer: session.accessToken, posterPath} : {}),
      ...(options && fence.kind !== 'channel' ? {
        chapters: options.chapters.slice(0, 500).filter(chapter => Number.isFinite(chapter.startMs) && chapter.startMs >= 0)
          .map(chapter => ({startSeconds: chapter.startMs / 1000, title: chapter.title.slice(0, 256)})),
        markers: options.markers.slice(0, 50).filter(marker => Number.isFinite(marker.startMs) && marker.endMs > marker.startMs)
          .map(marker => ({kind: marker.type, startSeconds: marker.startMs / 1000, endSeconds: marker.endMs / 1000})),
      } : {}),
      // Position, rate and play state come from the native player itself; publishing
      // them here would call setVideo on every progress tick.
      durationSeconds: state.playback.duration,
      live: fence.kind === 'channel', canNext: state.capabilities.next, canPrevious: state.capabilities.previous,
      skipBackSeconds: seconds('playback.skipBackMs', 10), skipForwardSeconds: seconds('playback.skipForwardMs', 30),
    };
    const nextKey = JSON.stringify(metadata);
    if (nextKey !== metadataKey) {port.setVideo(metadata); published = true; metadataKey = nextKey;}
  };
  const changed = () => {try {update();} catch (error) {if (current()) reportError(error);}};
  const subscriptions: (() => void)[] = [];
  const stop = () => {
    if (disposed) return;
    disposed = true;
    for (const off of subscriptions) off();
    clear();
  };
  try {
    subscriptions.push(playback.subscribe(changed));
    subscriptions.push(context.subscribeAuthority(changed));
    subscriptions.push(context.subscribePreferences(changed));
    changed();
  } catch (error) {stop(); throw error;}
  return stop;
}
