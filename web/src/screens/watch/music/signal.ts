import {musicSignal, type MusicSignalInput, type MusicSignalModel} from '@core/music-signal.ts';
import type {EqualizerSettings} from '../../../platform/playback/audio/equalizer';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import type {BrowserAudioFacts} from '../../../platform/playback/audio/output';

/** The drawn signal path for this song, from the server's own committed comparison,
 * the renderer's own observation and the device's EQ setting. Nothing is guessed:
 * an unreadable source has no line, and processing is only "applied" when the
 * current session's renderer reports it. Requested settings are not observations. */
export function watchMusicSignal(snapshot: WebPlaybackRuntimeSnapshot, equalizer: EqualizerSettings | null, browser?: BrowserAudioFacts): MusicSignalModel | undefined {
  const session = snapshot.session, observed = session?.observedPlayback?.audio;
  const effects = snapshot.playback.audioEffects;
  const pinned = session?.presentation.audio, current = snapshot.playback.session?.audio;
  // An audio comparison describes this accepted render recipe. A browser fallback
  // outside it cannot present the old recipe as the bytes it is now playing.
  const outsidePlan = session?.kind === 'audio' && (!effects.rendering || !pinned || !current
    || snapshot.playback.session?.id !== session.id || pinned.id !== current.id || pinned.mode !== current.mode
    || pinned.codec !== current.codec || pinned.sampleRate !== current.sampleRate
    || pinned.channels !== current.channels || pinned.bitDepth !== current.bitDepth);
  const info = outsidePlan ? undefined : session?.playbackInfo;
  const sourceAudio = info?.source.audio;
  const eqOn = equalizer?.enabled === true;
  const fallback = snapshot.playback.musicFallback === 'eq' ? 'eq' as const : undefined;
  if (!session || !info || !sourceAudio?.codec) {
    // No committed comparison: a real engine fallback still has a drawn line, from the
    // engine's own fact alone, and never a guessed format.
    if (!fallback) return undefined;
    return musicSignal({source: {}, output: {platform: 'browser', device: 'browser'}, eqOn, eqApplied: false, normalization: 'off', levelingApplied: false, fallback});
  }
  const source: MusicSignalInput['source'] = {codec: sourceAudio.codec, bitDepth: sourceAudio.bitDepth, sampleRate: sourceAudio.sampleRate,
    channels: sourceAudio.channels, layout: sourceAudio.layout, lossless: sourceAudio.lossless, atmos: sourceAudio.objectAudio === 'atmos'};
  const delivered = info.delivered.audio;
  const server: MusicSignalInput['server'] = {route: info.route,
    ...(delivered ? {delivered: {codec: delivered.codec, bitDepth: delivered.bitDepth, sampleRate: delivered.sampleRate, channels: delivered.channels, layout: delivered.layout, lossless: delivered.lossless}} : {}),
    // The shared label names Atmos specifically; a DTS:X loss is not that fact.
    ...(info.losses.some(loss => loss.feature === 'atmos') ? {atmosLost: true} : {}),
    ...(session.presentation.audio && typeof session.presentation.audio.loudnessAppliedDb === 'number' ? {loudnessAppliedDb: session.presentation.audio.loudnessAppliedDb} : {})};
  // Server conversion may already have changed the file's rate. Only a further
  // difference from the pinned delivered audio belongs to the browser output.
  const resampled = observed?.handling !== 'passthrough' && !!(observed?.sampleRate && delivered?.sampleRate && observed.sampleRate !== delivered.sampleRate);
  const base: MusicSignalInput['output'] = {platform: 'browser', device: 'browser',
    ...(observed?.sampleRate ? {sampleRate: observed.sampleRate} : {}), ...(observed?.bitDepth ? {bitDepth: observed.bitDepth} : {}),
    ...(observed?.handling === 'passthrough' ? {passthrough: true} : observed?.layout ? {decodedLayout: observed.layout} : {}),
    ...(resampled ? {resampledBy: 'browser' as const} : {})};
  // The engine's own measured output facts, only for this accepted render recipe. They state the
  // browser context rate and whether the browser resampled; they never claim a hardware rate, a
  // bit depth, passthrough, a layout or bit-perfect output, and any mismatch is refused outright.
  const local = localOutput(browser, outsidePlan, pinned);
  // A valid local pair replaces the whole output side: the observed bit depth, layout, passthrough
  // and older resampling flag describe another render and must not survive beside the engine's own
  // measured rate. Absent or refused facts keep the committed output exactly as before.
  const output: MusicSignalInput['output'] = local
    ? {platform: 'browser', device: 'browser', sampleRate: local.contextRate, bitPerfect: false, ...(local.resampled ? {resampledBy: 'browser' as const} : {})}
    : base;
  return musicSignal({source, server, output, eqOn, eqApplied: effects.eqApplied,
    normalization: effects.settings.normalization, levelingApplied: effects.levelingApplied, levelingGainDb: effects.levelingGainDb, ...(fallback ? {fallback} : {})});
}

/** The browser's own facts are used only when they describe this exact accepted recipe: the
 *  engine's own web-audio output, no claimed hardware rate, no bit-perfect output, finite
 *  source and context rates that match the pinned delivered audio, and a resampling flag that
 *  agrees with those rates. Anything else is refused, so the drawn path falls back to the
 *  committed comparison exactly as before. */
function localOutput(facts: BrowserAudioFacts | undefined, outsidePlan: boolean, pinned: {sampleRate?: number; channels?: number} | undefined) {
  if (!facts || outsidePlan || !pinned) return undefined;
  if (facts.engine !== 'web-audio' || facts.bitPerfect !== false || facts.deviceRate !== null) return undefined;
  const {sourceRate, contextRate, resampledToContext, sourceChannels} = facts;
  if (!Number.isFinite(sourceRate) || sourceRate <= 0 || !Number.isFinite(contextRate) || contextRate <= 0) return undefined;
  if (sourceRate !== pinned.sampleRate || sourceChannels !== pinned.channels) return undefined;
  if (resampledToContext !== (sourceRate !== contextRate)) return undefined;
  return Object.freeze({contextRate, resampled: resampledToContext});
}
