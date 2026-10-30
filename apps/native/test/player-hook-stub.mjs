// Runtime boundaries only. The player hook, coordinator and shared playback service stay real.
/** @type {{session: any, stack: any}} */
export const control = {session: undefined, stack: undefined};
export const useSession = () => control.session;
export const useNativePlayback = () => control.stack;
const off = () => () => {};
export const useNativeAudioSelection = () => ({command: null, coordinator: {acceptFact: () => false, retryMapping() {}}});
export const useServerPreferences = () => ({snapshot: undefined, value: (_key, fallback) => fallback});
export const usePreferences = () => ({preferences: {playerEngine: 'automatic'}});
export const mpvAvailable = false;
export const engineFeatures = () => ({subtitleRendering: ['text'], audioTrackSwitching: false});
export const engineTransport = () => undefined;
export const showToast = () => {};
export const activeI18n = () => ({t: key => key});
export const topShelfChanged = () => {};

export const viewerScope = () => undefined;
