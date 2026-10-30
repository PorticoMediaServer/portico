import {Platform} from 'react-native';
import type {NativePlaybackRuntimeSnapshot} from '../../../platform/playback/types';
import type {DeviceEqualizer} from '../../../platform/playback/equalizer';
import type {MusicFormatFacts, MusicOutputFacts, MusicServerSignal, MusicSignalInput} from '../../../../../../packages/client-core/src/music-signal';
import type {PlaybackSoundFacts} from '../../../../../../packages/client-core/src/playback-v1/playback-info';

function format(facts: PlaybackSoundFacts | undefined): MusicFormatFacts | undefined {
  if (!facts?.codec) return undefined;
  const codec = facts.codec.toLowerCase();
  return Object.freeze({
    codec, ...(facts.lossless !== undefined ? {lossless: facts.lossless} : {}),
    ...(facts.bitDepth ? {bitDepth: facts.bitDepth} : {}), ...(facts.sampleRate ? {sampleRate: facts.sampleRate} : {}),
    ...(facts.channels ? {channels: facts.channels} : {}), ...(facts.layout ? {layout: facts.layout} : {}),
    ...(facts.objectAudio === 'atmos' ? {atmos: true} : {}),
  });
}

/** music-plus (MC-8): the line and sheet describe the committed plan and the engine's own
 * current-song facts. Nothing here infers an applied effect from a setting: the EQ's applied
 * fact is the listening snapshot's, and an unreported rate is never a matched one. */
export function nativeMusicSignal(snapshot: Pick<NativePlaybackRuntimeSnapshot, 'session' | 'playback'>, equalizer: DeviceEqualizer | undefined): MusicSignalInput | undefined {
  const info = snapshot.session?.playbackInfo, listening = snapshot.playback.nativeListening;
  const effects = snapshot.playback.audioEffects;
  const source = format(info?.source.audio);
  if (!source) return undefined;
  const delivered = format(info?.delivered.audio);
  const atmosLost = info?.losses?.some(loss => loss.feature === 'atmos') === true;
  const dsdConverted = info?.route === 'converted' && !!info.source.audio?.codec?.toLowerCase().startsWith('dsd_');
  const serverGain = snapshot.session?.presentation.audio?.loudnessAppliedDb;
  const server: MusicServerSignal | undefined = info ? Object.freeze({
    route: info.route,
    ...(delivered ? {delivered} : {}),
    ...(dsdConverted ? {dsdConverted: true} : {}),
    ...(atmosLost ? {atmosLost: true} : {}),
    ...(serverGain !== undefined ? {loudnessAppliedDb: serverGain} : {}),
  }) : undefined;
  // The output route is an engine observation, never a setting: an unreported rate is left out.
  const observed = snapshot.session?.observedPlayback?.audio;
  const platform: MusicOutputFacts['platform'] = Platform.OS === 'ios' ? 'apple' : Platform.OS === 'android' ? 'android' : 'other';
  const sampleRate = observed?.sampleRate && Number.isFinite(observed.sampleRate) ? observed.sampleRate : undefined;
  const output: MusicOutputFacts = Object.freeze({
    platform, device: Platform.isTV ? 'tv' : 'phone',
    ...(observed?.handling === 'passthrough' ? {passthrough: true} : {}),
    ...(sampleRate ? {sampleRate} : {}),
    ...(observed?.bitDepth ? {bitDepth: observed.bitDepth} : {}),
    ...(observed?.layout ? {decodedLayout: observed.layout} : {}),
    // Apple never claims bit-perfect; it says the rate is matched, and only when the engine
    // reported an output rate equal to the file's own.
    ...(platform === 'apple' && sampleRate && source.sampleRate === sampleRate ? {sampleRateMatched: true} : {}),
  });
  return Object.freeze({
    source, ...(server ? {server} : {}), output,
    // Requested effects come from the profile's own audio settings; applied facts come only from
    // the engine for this song. A requested effect with no applied fact is "not applied", never
    // "applied" and never silently "off".
    eqOn: equalizer?.on === true, eqApplied: (listening?.eqApplied ?? effects.eqApplied) === true,
    normalization: effects.settings.normalization,
    levelingApplied: (listening?.levelingApplied ?? effects.levelingApplied) === true,
    ...(effects.levelingGainDb !== undefined ? {levelingGainDb: effects.levelingGainDb} : {}),
    // The engine's own lasting fallback for this song: the line says what was lost. Unreported
    // stays absent, so nothing invents a fallback.
    ...(snapshot.playback.musicFallback === 'eq' ? {fallback: 'eq' as const} : {}),
  });
}
