/**
 * A media element, a media source and its buffers, as far as the Vega bridge uses them.
 *
 * An initialization segment has an ISO BMFF `moov` box. A media "segment" is a few
 * bytes of JSON saying `{pts, duration}` for media that starts at `pts` seconds on its own
 * clock. A buffer in `segments` mode keeps that clock; in `sequence` mode each segment follows the
 * one before it, as the real thing does. `timestampOffset` moves that clock: in `sequence` mode the
 * next segment begins where the offset is set and the ones after it follow on, and in `segments`
 * mode the offset is added to each segment's own time — the rule the element follows.
 * `world.sequenceMode = false` makes a device that refuses `sequence`; `world.refuse` makes one
 * that will not take a type.
 */
export const world = {sequenceMode: true, refuse: /** @type {RegExp | null} */ (null), players: /** @type {VideoPlayer[]} */ ([]), appendFails: false, seekClamp: /** @type {number | undefined} */ (undefined)};
export function resetWorld() { world.sequenceMode = true; world.refuse = null; world.players = []; world.appendFails = false; world.seekClamp = undefined; }

class Target {
  listeners = new Map();
  addEventListener(type, listener) { if (!this.listeners.has(type)) this.listeners.set(type, new Set()); this.listeners.get(type).add(listener); }
  removeEventListener(type, listener) { this.listeners.get(type)?.delete(listener); }
  emit(type) { for (const listener of [...(this.listeners.get(type) ?? [])]) listener({type}); }
}

const ranges = spans => ({length: spans.length, start: i => spans[i][0], end: i => spans[i][1]});

class SourceBuffer extends Target {
  spans = [];
  updating = false;
  appended = [];
  #mode = 'segments';
  #offset = 0;
  #groupEnd = 0;
  constructor(source, type) { super(); this.source = source; this.type = type; }
  get mode() { return this.#mode; }
  set mode(value) { if (value === 'sequence' && !world.sequenceMode) throw new Error('sequence mode is not supported'); this.#mode = value; }
  get buffered() { return ranges(this.spans); }
  get timestampOffset() { return this.#offset; }
  /** Where appended timestamps are shifted to: the start of the next `sequence`-mode group, and
   * the shift applied to `segments`-mode frames. */
  set timestampOffset(value) {
    if (!Number.isFinite(value) || value < 0) throw new TypeError('the timestamp offset is not a finite time');
    this.#offset = value;
    this.#groupEnd = value;
  }
  #add(start, end) {
    const all = [...this.spans, [start, end]].sort((a, b) => a[0] - b[0]);
    const merged = [];
    for (const span of all) { const last = merged[merged.length - 1]; if (last && span[0] <= last[1] + 0.001) last[1] = Math.max(last[1], span[1]); else merged.push([...span]); }
    this.spans = merged;
  }
  appendBuffer(data) {
    if (this.updating) throw new Error('InvalidStateError: the buffer is updating');
    const bytes = Buffer.from(data);
    const segment = bytes.length >= 8 && bytes.readUInt32BE(0) === bytes.length && bytes.toString('ascii', 4, 8) === 'moov'
      ? {init: true} : JSON.parse(bytes.toString('utf8'));
    this.updating = true;
    setTimeout(() => {
      this.updating = false;
      if (world.appendFails) { this.emit('error'); return; }
      this.appended.push(segment);
      if (!segment.init) {
        const start = this.#mode === 'sequence' ? this.#groupEnd : segment.pts + this.#offset;
        this.#groupEnd = start + segment.duration;
        // In sequence mode the element keeps the offset at the end of what it placed, so the
        // segment after this one follows on from it.
        if (this.#mode === 'sequence') this.#offset = this.#groupEnd;
        this.#add(start, start + segment.duration);
        this.source.player?.described();
      }
      this.emit('updateend');
    }, 0);
  }
  remove(from, to) {
    if (this.updating) throw new Error('InvalidStateError: the buffer is updating');
    this.updating = true;
    setTimeout(() => {
      this.updating = false;
      this.spans = this.spans.flatMap(([start, end]) => (to <= start || from >= end ? [[start, end]] : [[start, Math.min(end, from)], [Math.max(start, to), end]].filter(([a, b]) => b - a > 0.001)));
      this.emit('updateend');
    }, 0);
  }
}

export class MediaSource extends Target {
  static isTypeSupported(type) { return !world.refuse?.test(type); }
  readyState = 'closed';
  duration = NaN;
  buffers = [];
  player = null;
  addSourceBuffer(type) {
    if (world.refuse?.test(type)) throw new Error('NotSupportedError: ' + type);
    const buffer = new SourceBuffer(this, type);
    this.buffers.push(buffer);
    return buffer;
  }
  removeSourceBuffer(buffer) {
    if (buffer.updating || !this.buffers.includes(buffer)) throw new Error('InvalidStateError: buffer cannot be removed');
    this.buffers.splice(this.buffers.indexOf(buffer), 1);
  }
  endOfStream() { this.readyState = 'ended'; this.player?.sourceEnded(); }
}

export class VideoPlayer extends Target {
  paused = true;
  autoplay = false;
  error = null;
  initialized = false;
  surface = null;
  source = null;
  #time = 0;
  #src = '';
  #described = false;
  constructor() { super(); world.players.push(this); }
  initialize() { this.initialized = true; return Promise.resolve(); }
  deinitialize() { this.initialized = false; return Promise.resolve(); }
  setSurfaceHandle(handle) { this.surface = handle; }
  clearSurfaceHandle() { this.surface = null; }
  setMediaControlFocus() { return Promise.resolve(); }
  get src() { return this.#src; }
  set src(value) { this.#src = value; setTimeout(() => this.described(), 0); }
  set srcObject(source) { this.source = source; source.player = this; source.readyState = 'open'; setTimeout(() => source.emit('sourceopen'), 0); }
  get buffered() { return ranges(this.source?.buffers[0]?.spans ?? (this.#src ? [[0, 3600]] : [])); }
  get currentTime() { return this.#time; }
  set currentTime(value) { this.#time = world.seekClamp === undefined ? value : world.seekClamp; setTimeout(() => this.emit('seeked'), 0); }
  /** The test moves time on by hand, as playback would. */
  advance(seconds) { this.#time += seconds; }
  described() { if (this.#described) return; this.#described = true; this.emit('loadedmetadata'); }
  play() { this.paused = false; setTimeout(() => this.emit('playing'), 0); return Promise.resolve(); }
  pause() { if (this.paused) return; this.paused = true; setTimeout(() => this.emit('pause'), 0); }
  sourceEnded() {}
  fail(code) { this.error = {code}; this.emit('error'); }
}

export class KeplerMediaControlHandler {}
export const KeplerVideoSurfaceView = 'KeplerVideoSurfaceView';
export const decodingInfo = async () => ({supported: true, smooth: false, powerEfficient: true});
