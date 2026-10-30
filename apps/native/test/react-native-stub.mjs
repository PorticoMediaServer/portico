/**
 * The smallest `react-native` surface the downloads bridge touches, backed by an
 * in-memory device.
 *
 * `PorticoDownloads` mirrors the real native module's contract: a manifest of
 * entries with their JSON `meta`, a state blob, and removal that takes the file
 * with it. Tests read `device` to assert what actually happened on disk, which
 * is the thing APP-01 is about — a refused renewal used to delete another
 * viewer's bytes here.
 */
export const device = {
  /** @type {Map<string, any>} */ entries: new Map(),
  /** @type {string} */ state: '',
  /** @type {string[]} */ removed: [],
  /** @type {any[]} */ started: [],
  /** @type {boolean|undefined} */ cellular: undefined,
  /** @type {{id: string, startSeconds: number}[]} */ played: [],
  reset() {
    this.entries = new Map();
    this.state = '';
    this.removed = [];
    this.started = [];
    this.cellular = undefined;
    this.played = [];
  },
  /** Seeds a finished download exactly as the native module would report it. */
  seed(entry) {
    const {meta, ...rest} = entry;
    this.entries.set(entry.id, {
      state: 'ready', error: '', bytes: 0, bytesDone: 0, addedAt: 0,
      title: '', subtitle: '', quality: '', artworkUrl: '',
      ...rest,
      meta: JSON.stringify(meta ?? {}),
    });
  },
  metaOf(id) {
    const raw = this.entries.get(id)?.meta;
    return raw ? JSON.parse(raw) : undefined;
  },
};

const listeners = new Map();

/** Receipt signature checking. Tests set `accept` to decide which signatures
 * verify, so an unverifiable receipt can be exercised without real Ed25519. */
export const crypto = {accept: () => true};

export const NativeModules = {
  PorticoNetwork: {
    async sha256() { return btoa('\0'.repeat(32)); },
    async verify() { return crypto.accept(); },
    startMonitoring() {},
    cancelBrowse() {},
    async browse() { return []; },
  },
  // Open-ended: a test installs (and removes) the runtime methods the bridge under test calls.
  /** @type {Record<string, any>} */
  PorticoRuntime: {
    playbackRequestId: () => 'req',
    playbackRandomBytes: n => Array.from({length: n}, () => 0),
    cancelQueueRequest() {},
    async networkRequest() { return {}; },
  },
  PorticoDownloads: {
    async list() { return [...device.entries.values()].map(e => ({...e})); },
    async start(request) {
      device.started.push(request);
      device.entries.set(request.id, {
        id: request.id, itemId: request.itemId, title: request.title, subtitle: request.subtitle,
        quality: request.quality, artworkUrl: request.artworkUrl, state: 'downloading', error: '',
        bytes: request.bytes, bytesDone: 0, addedAt: 0, meta: request.meta,
      });
    },
    async pause() {},
    async remove(id) { device.removed.push(id); device.entries.delete(id); },
    async setMeta(id, meta) {
      const entry = device.entries.get(id);
      if (entry) device.entries.set(id, {...entry, meta});
    },
    async storage() { return {freeBytes: 1e12, usedBytes: 0}; },
    async setCellular(allowed) { device.cellular = allowed; },
    async readState() { return device.state; },
    async writeState(json) { device.state = json; },
    async localFile(id) { return {url: `file:///downloads/${id}/media`, durationSeconds: 0}; },
  },
};

export class NativeEventEmitter {
  addListener(name, handler) {
    const set = listeners.get(name) ?? new Set();
    set.add(handler);
    listeners.set(name, set);
    return {remove: () => set.delete(handler)};
  }
}

/** Delivers a native event the way the real emitter would. */
export function emit(name, payload) {
  for (const handler of listeners.get(name) ?? []) handler(payload);
}

export const Platform = {OS: 'ios', isTV: false};

const linkHandlers = new Set();
/** Links from outside the app. A test replaces `getInitialURL` and calls `deliver` for a `url` event. */
export const Linking = {
  /** @type {() => Promise<string | null>} */
  getInitialURL: async () => null,
  addEventListener: (_type, handler) => { linkHandlers.add(handler); return {remove: () => linkHandlers.delete(handler)}; },
  deliver(url) { for (const handler of [...linkHandlers]) handler({url}); },
};

export const AppState = {
  currentState: 'active',
  addEventListener: () => ({remove: () => {}}),
};

/** What the app asked the screen reader to say, in order. */
export const spoken = [];
export const AccessibilityInfo = {
  announceForAccessibility: text => { spoken.push(text); },
  announceForAccessibilityWithOptions: text => { spoken.push(text); },
  isScreenReaderEnabled: async () => false,
  isReduceMotionEnabled: async () => false,
  addEventListener: () => ({remove: () => {}}),
};

/** Logical viewport for the next theme's pure projection tests. */
const logicalWindow = Object.freeze({width: 390, height: 844, scale: 1, fontScale: 1});
export const Dimensions = {get: name => {
  if (name !== 'window' && name !== 'screen') throw new Error('unknown_dimension');
  return logicalWindow;
}};
export const useWindowDimensions = () => logicalWindow;

export const useTVEventHandler = () => {};
export const TVEventHandler = {addListener: handler => new NativeEventEmitter().addListener('onHWKeyEvent', handler)};

/** Only explicitly attached controlled hosts have native handles in source tests. */
export const findNodeHandle = host => host?.nativeTag ?? null;

// BottomSheet subscribes only for explicitly opted-in native text-entry sheets.
export const Keyboard = {isVisible: () => false, dismiss() {}, addListener: () => ({remove() {}})};
export const LayoutAnimation = {configureNext() {}, Types: {keyboard: "keyboard"}};
