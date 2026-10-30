// Vega's W3C engine has no separate native listening owner or audio effects.
export {serverPlaybackV1Kinds} from '../../../../../../native/src-next/platform/playback/native-listening-kinds';
export const nativeV1ListeningKinds: readonly string[] = Object.freeze([]);
export function nativeListeningFor(serverKinds: readonly string[], enabled: readonly string[] = nativeV1ListeningKinds) {
  return (kind: string) => serverKinds.includes(kind) && enabled.includes(kind);
}
export const playerSettingsSupport = Object.freeze({playbackSpeed: false, audioEffects: false});
