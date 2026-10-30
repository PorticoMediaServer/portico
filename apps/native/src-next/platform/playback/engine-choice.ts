/**
 * Which player engine plays a title: the platform's own player ("native": AVPlayer, ExoPlayer) or
 * mpv. Platform-neutral and free of React and native code, so every platform decides the same way.
 *
 * The viewer's Player setting is Automatic (the default), Native or mpv.
 *
 * 1. Some things only the native engine plays (music and audiobooks, which belong to the native
 *    listening owner, and live channels). They go there whatever the setting says. So does
 *    everything on a device whose build has no mpv.
 * 2. A subtitle track the server delivers for the device to draw, in a kind the native engine does
 *    not draw (styled ASS, image subtitles), needs mpv, unless the viewer chose Native.
 * 3. When the server's plan names an engine (`engine`: "native" or "mpv"), that is the engine. The
 *    server chose it from this device's capability document, which carries one profile per engine
 *    and the viewer's setting, and it made the stream for that engine.
 * 4. Otherwise the local rule decides, from what the device itself can tell:
 *    - Native or mpv, when the viewer chose one.
 *    - Automatic: the native player when it can play what is in hand (the file directly, or a
 *      plain repackaging the server already made); mpv when the native player cannot open it and
 *      the only alternative would be a conversion. A stream the server has already converted was
 *      made for the native player and plays there: choosing mpv for it would gain nothing and
 *      lose Picture in Picture and AirPlay.
 */
export type EngineKind = 'native' | 'mpv';
export type EngineSetting = 'automatic' | 'native' | 'mpv';

export const engineSettings: readonly EngineSetting[] = ['automatic', 'native', 'mpv'];
export const defaultEngineSetting: EngineSetting = 'automatic';

export function parseEngineSetting(value: unknown): EngineSetting {
  return value === 'native' || value === 'mpv' ? value : 'automatic';
}

/** What the native player would have to be given to play the source in hand. */
export type NativeNeed =
  /** It plays the source as it is, or as a repackaging that copies every stream. */
  | 'nothing'
  /** The picture would have to be converted (a codec, bit depth or container it does not take, with no copy route). */
  | 'video-conversion'
  /** The sound would have to be converted to a lossy codec. */
  | 'lossy-audio'
  /** The chosen subtitles would have to be burned into the picture. */
  | 'burned-subtitles';

export type EngineFacts = Readonly<{
  setting: EngineSetting;
  /** This build has the mpv engine and it started. */
  mpvAvailable: boolean;
  /** The engine the server's plan named, when it named one. */
  planEngine?: unknown;
  /** Music, an audiobook or a live channel: only the native engine plays it. */
  nativeOnly?: boolean;
  /** The kind (ass, pgs, vobsub, dvb) of a chosen subtitle track the device itself must draw. */
  requiredSubtitleRendering?: string;
  /** The subtitle kinds the native engine draws itself. Absent: plain text only. */
  nativeSubtitleRendering?: readonly string[];
  /** Local rule only. Absent: nothing is known, which reads as "the native player can play it". */
  nativeNeeds?: NativeNeed;
  /** Local rule only. False when mpv is known not to play the source either. */
  mpvCanPlay?: boolean;
}>;

export function chooseEngine(facts: EngineFacts): EngineKind {
  if (!facts.mpvAvailable || facts.nativeOnly) return 'native';
  if (facts.requiredSubtitleRendering && facts.setting !== 'native' && !(facts.nativeSubtitleRendering ?? ['text']).includes(facts.requiredSubtitleRendering)) return 'mpv';
  if (facts.planEngine === 'native' || facts.planEngine === 'mpv') return facts.planEngine;
  if (facts.setting === 'native') return 'native';
  if (facts.setting === 'mpv') return 'mpv';
  if (!facts.nativeNeeds || facts.nativeNeeds === 'nothing') return 'native';
  return facts.mpvCanPlay === false ? 'native' : 'mpv';
}

/** Containers AVPlayer opens as a file (everything else it must be given as HLS). */
const appleFileContainers = new Set(['mp4', 'm4v', 'mov', 'm4a', 'm4b', 'mp3', 'aac', 'flac', 'wav', 'aiff', 'caf']);
/** Containers Media3 opens as a file. */
const androidFileContainers = new Set(['mp4', 'm4v', 'mov', 'mkv', 'webm', 'ts', 'm2ts', 'mp3', 'm4a', 'm4b', 'aac', 'flac', 'ogg', 'opus', 'wav']);

/** A container as the server names it ("matroska,webm", "mov,mp4,m4a,3gp,3g2,mj2", "mpegts"), as the extension it is known by here. */
function containerName(value: string | undefined): string {
  const names = (value ?? '').toLowerCase().split(',').map(n => n.trim()).filter(Boolean);
  if (!names.length) return '';
  if (names.some(n => n === 'matroska' || n === 'mkv')) return 'mkv';
  if (names.includes('webm')) return 'webm';
  if (names.some(n => n === 'mp4' || n === 'mov' || n === 'm4v')) return 'mp4';
  if (names.some(n => n === 'mpegts' || n === 'ts')) return 'ts';
  return names[0]!;
}

function extensionOf(address: string): string {
  const path = address.split(/[?#]/)[0] ?? '';
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

/**
 * The local rule's facts for a source the server did not plan an engine for.
 *
 * - A file on this device (a download): the native player needs a conversion when it cannot open
 *   the file's container, and nothing can convert it here, so mpv plays it.
 * - A server stream with no engine named (`delivery`: "direct" or "hls"): it was planned for the
 *   native player, which therefore needs nothing.
 */
export function localRuleFacts(source: Readonly<{address: string; local: boolean; platform: 'apple' | 'android'; /** The file's container when what came with it says (a download's own description). */ container?: string}>): Readonly<{nativeNeeds: NativeNeed; mpvCanPlay: boolean}> {
  if (!source.local) return {nativeNeeds: 'nothing', mpvCanPlay: true};
  const container = containerName(source.container) || extensionOf(source.address);
  const opens = (source.platform === 'apple' ? appleFileContainers : androidFileContainers).has(container);
  return {nativeNeeds: opens || !container ? 'nothing' : 'video-conversion', mpvCanPlay: true};
}

/**
 * What an engine does not do, as its module reports it (`features`, ENGINE-CONTRACT.md). Absent
 * (an older native build): the native engine's own abilities.
 */
export type EngineFeatures = Readonly<{
  pictureInPicture: boolean;
  airPlayVideo: boolean;
  liveChannels: boolean;
  musicSessions: boolean;
  /** A direct-played file's audio track is switched inside the engine, without a new session. */
  audioTrackSwitching: boolean;
  /** Subtitle kinds the engine draws itself: text, ass, pgs, vobsub, dvb. */
  subtitleRendering: readonly string[];
  chapters: boolean;
}>;

export const nativeEngineFeatures: EngineFeatures = Object.freeze({pictureInPicture: true, airPlayVideo: true, liveChannels: true, musicSessions: true, audioTrackSwitching: false, subtitleRendering: Object.freeze(['text']), chapters: false});

export function parseEngineFeatures(value: unknown, fallback: EngineFeatures = nativeEngineFeatures): EngineFeatures {
  if (!value || typeof value !== 'object') return fallback;
  const v = value as Record<string, unknown>;
  const flag = (key: keyof EngineFeatures) => (typeof v[key] === 'boolean' ? (v[key] as boolean) : (fallback[key] as boolean));
  const kinds = Array.isArray(v.subtitleRendering) ? v.subtitleRendering.filter((k): k is string => typeof k === 'string' && ['text', 'ass', 'pgs', 'vobsub', 'dvb'].includes(k)) : fallback.subtitleRendering;
  return Object.freeze({pictureInPicture: flag('pictureInPicture'), airPlayVideo: flag('airPlayVideo'), liveChannels: flag('liveChannels'), musicSessions: flag('musicSessions'), audioTrackSwitching: flag('audioTrackSwitching'), subtitleRendering: Object.freeze([...kinds]), chapters: flag('chapters')});
}
