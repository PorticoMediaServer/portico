import type {DownloadTitle} from '@core/downloads.ts';
import type {PlayerEvent, PlayerObservation} from '@core/playback-v1/port.ts';
import {localPlayerSource, localPlayerLoadReceipt, type LocalPlayerLoadContext} from '@core/playback-v1/local-port.ts';
import {localPlaybackFence, localPlaybackScope, sameLocalScope, sameFence, type LocalPlaybackScope, type LocalPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {PlayerSlot} from './web-playback.ts';

export type WebLocalQueueMember = Readonly<{id: string; revision: number; itemId: string; title: DownloadTitle; durationMs: number; localSourceId: string}>;
export type WebLocalQueuePreparation = Readonly<{preparationId: string; scope: LocalPlaybackScope; selectionRevision: string; items: readonly WebLocalQueueMember[]}>;
/** Runtime takes ownership through stop, replacement or retained paused restoration.
 * release retires only this app-owned preparation, never another queue's handles. */
export type WebLocalQueueResource = Readonly<{preparation: WebLocalQueuePreparation; release(): Promise<void>;
  /** Part of the offline-resource contract (O-159). Supplied by the launch host: prepares a FRESH
   * resource (new handle or blob URL) for the same items, itself carrying reacquire. Try again
   * after a watchdog stop uses it, because the stopped start's resource is already released. A
   * host without it gets the named failure `local_playback_unavailable`, never a silent no-op.
   * No web path builds local resources today; the planned desktop host must implement it. */
  reacquire?(): Promise<WebLocalQueueResource>}>;
export type WebLocalQueueSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'ended' | 'error' | 'disposed'; preparation: WebLocalQueuePreparation; index: number;
  fence?: LocalPlaybackFence; observation?: PlayerObservation; problem?: string;
}>;
type Port = Pick<PlayerSlot, 'canLoadLocal' | 'loadLocal' | 'loaded' | 'settling' | 'observe' | 'onEvent' | 'play' | 'pause' | 'seek' | 'unload'>;
function unavailable(): never {throw new Error('local_playback_unavailable');}
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(value);
/** This is an already parsed app model, not GET/downloads wire units. Preserve its actual
 * seconds and approved metadata only; never pass it back through a millisecond wire parser. */
function captureTitle(value: DownloadTitle): DownloadTitle {
  if (!value || !['song', 'episode', 'movie', 'extra', 'audiobook_file'].includes(value.kind)
    || typeof value.title !== 'string' || !value.title.trim() || value.title.length > 1024
    || !Number.isSafeInteger(value.year) || value.year < 0 || !Number.isFinite(value.durationSeconds) || value.durationSeconds < 0
    || typeof value.onServer !== 'boolean') unavailable();
  for (const field of ['parentTitle', 'groupTitle', 'artist'] as const) if (typeof value[field] !== 'string' || value[field].length > 1024) unavailable();
  for (const field of ['parentId', 'groupId'] as const) if (value[field] !== '' && !id(value[field])) unavailable();
  for (const field of ['groupNumber', 'number'] as const) if (value[field] !== null && (!Number.isSafeInteger(value[field]) || value[field]! < 0)) unavailable();
  return Object.freeze({kind: value.kind, title: value.title, year: value.year, durationSeconds: value.durationSeconds,
    parentId: value.parentId, parentTitle: value.parentTitle, groupId: value.groupId, groupTitle: value.groupTitle,
    groupNumber: value.groupNumber, number: value.number, artist: value.artist, onServer: value.onServer});
}


/** The app has verified the complete group before handing this ordered preparation over.
 * The runtime preserves that order; each native load rechecks its member and receipt again. */
export function captureLocalQueue(value: WebLocalQueuePreparation, scope: LocalPlaybackScope): WebLocalQueuePreparation {
  if (!value || !id(value.preparationId) || typeof value.selectionRevision !== 'string' || !value.selectionRevision || value.selectionRevision.length > 256
    || !sameLocalScope(localPlaybackScope(value.scope), scope) || !Array.isArray(value.items) || !value.items.length) unavailable();
  const entries = new Set<string>(), sources = new Set<string>();
  const items = value.items.map(row => {
    if (!row || ![row.id, row.itemId, row.localSourceId].every(id) || !Number.isSafeInteger(row.revision) || row.revision < 0
      || !Number.isSafeInteger(row.durationMs) || row.durationMs < 0 || entries.has(row.id) || sources.has(row.localSourceId)) unavailable();
    const title = captureTitle(row.title);
    if (Math.round(title.durationSeconds * 1000) !== row.durationMs) unavailable();
    entries.add(row.id);sources.add(row.localSourceId);
    return Object.freeze({id: row.id, revision: row.revision, itemId: row.itemId, title: Object.freeze({...title}), durationMs: row.durationMs, localSourceId: row.localSourceId});
  });
  return Object.freeze({preparationId: value.preparationId, scope, selectionRevision: value.selectionRevision, items: Object.freeze(items)});
}

/** Ordered local execution over the SAME engine slot. No V1 session, server queue, URL,
 * wall-clock progress, inferred artifact or fabricated journal identity exists here. */
export class WebLocalQueue {
  private snapshot: WebLocalQueueSnapshot;
  private serial = 0;
  private generation = 0;
  private disposed = false;
  private moving = false;
  private readonly life = new AbortController();
  private readonly listeners = new Set<() => void>();
  private readonly off: () => void;
  private releaseTask?: Promise<void>;
  constructor(private readonly options: Readonly<{scope: LocalPlaybackScope; run: number; port: Port; preparation: WebLocalQueuePreparation;
    isCurrent(): boolean; release(): Promise<void>; reportError(error: unknown): void}>) {
    const scope = localPlaybackScope(options.scope), preparation = captureLocalQueue(options.preparation, scope);
    if (!options.port.canLoadLocal || !Number.isSafeInteger(options.run) || options.run < 1 || !options.isCurrent()) unavailable();
    this.snapshot = Object.freeze({phase: 'idle', preparation, index: 0});
    this.off = options.port.onEvent(event => this.event(event));
  }
  getSnapshot = (): WebLocalQueueSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private current() {return !this.disposed && !this.life.signal.aborted && this.options.isCurrent();}
  private publish(value: Partial<WebLocalQueueSnapshot>) {this.snapshot = Object.freeze({...this.snapshot, ...value});for (const listener of [...this.listeners]) listener();}
  private require(fence: LocalPlaybackFence) {if (!this.current() || !sameFence(this.snapshot.fence, fence) || this.moving) throw new Error('stale_playback_command');}
  async start(context: LocalPlayerLoadContext, options: Readonly<{index?: number; startPositionMs?: number; autoplay?: boolean}> = {}): Promise<void> {
    if (this.snapshot.phase !== 'idle') unavailable();await this.load(options.index ?? 0, context, options.startPositionMs ?? 0, options.autoplay ?? true);
  }
  private async load(index: number, context?: LocalPlayerLoadContext, startPositionMs = 0, autoplay = true): Promise<void> {
    if (!this.current() || !this.options.port.canLoadLocal || !Number.isInteger(index) || index < 0 || index >= this.snapshot.preparation.items.length) unavailable();
    if (context?.signal.aborted || context?.isCurrent() === false) throw new DOMException('Local selection retired', 'AbortError');
    const serial = ++this.serial, member = this.snapshot.preparation.items[index]!, generation = ++this.generation;
    const admission = new AbortController();
    const abort = () => admission.abort();
    context?.signal.addEventListener('abort', abort, {once: true});
    const signal = AbortSignal.any([admission.signal, this.life.signal]);
    let adopted = false;
    // The initiating action owns admission only. After the real receipt is adopted,
    // the queue/runtime lifetime owns playback and retained bookmark restoration.
    const current = () => this.current() && serial === this.serial && !signal.aborted && (adopted || context?.isCurrent() !== false);
    this.moving = true;this.publish({phase: 'loading', index, fence: undefined, observation: undefined, problem: undefined});
    try {
      const source = localPlayerSource({kind: 'local', scope: this.snapshot.preparation.scope, preparationId: this.snapshot.preparation.preparationId,
        localSourceId: member.localSourceId, entryId: member.id, itemId: member.itemId, generation, startPositionMs, autoplay});
      const receipt = localPlayerLoadReceipt(await this.options.port.loadLocal(source, {signal, isCurrent: current}), source);
      if (!current()) throw new DOMException('Local selection retired', 'AbortError');
      adopted = true;context?.signal.removeEventListener('abort', abort);
      const fence = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: receipt.scope, run: this.options.run}, sessionId: receipt.sessionId,
        generation: receipt.generation, itemId: receipt.itemId, entryId: receipt.entryId, playId: receipt.playId, artifact: receipt.artifact});
      this.moving = false;this.publish({phase: 'ready', fence});this.read(this.options.port.observe());
    } catch (error) {
      if (serial === this.serial && !this.disposed) {this.moving = false;this.publish({phase: 'error', fence: undefined, observation: undefined, problem: 'local_playback_failed'});}
      throw error;
    } finally {context?.signal.removeEventListener('abort', abort);}
  }
  private read(observation: PlayerObservation) {
    const fence = this.snapshot.fence;
    if (!this.current() || this.moving || !fence || observation.generation !== fence.generation || observation.state !== 'error' && this.options.port.loaded !== fence.generation
      || !Number.isFinite(observation.positionMs) || observation.positionMs < 0 || !Number.isFinite(observation.rate) || observation.rate <= 0) return;
    this.publish({observation: Object.freeze({...observation}), phase: observation.state === 'error' ? 'error' : observation.state === 'ended' ? 'ended' : 'ready', problem: observation.state === 'error' ? 'local_playback_failed' : undefined});
  }
  private event(event: PlayerEvent) {
    this.read(event.observation);
    const fence = this.snapshot.fence;
    if (!fence || this.snapshot.observation?.state !== 'ended' || event.observation.generation !== fence.generation || this.moving || this.snapshot.index + 1 >= this.snapshot.preparation.items.length) return;
    this.moving = true;
    queueMicrotask(() => {
      if (!this.current() || !sameFence(this.snapshot.fence, fence)) return;
      void this.load(this.snapshot.index + 1).catch(error => {if (this.current()) this.options.reportError(error);});
    });
  }
  pause(fence: LocalPlaybackFence) {this.require(fence);this.options.port.pause();}
  resume(fence: LocalPlaybackFence) {this.require(fence);this.options.port.play();}
  seek(fence: LocalPlaybackFence, seconds: number) {
    this.require(fence);
    if (!Number.isFinite(seconds) || seconds < 0) unavailable();
    this.options.port.seek(Math.min(seconds * 1000, this.snapshot.preparation.items[this.snapshot.index]!.durationMs));
  }
  async next(fence: LocalPlaybackFence) {this.require(fence);await this.load(this.snapshot.index + 1);}
  async previous(fence: LocalPlaybackFence) {
    this.require(fence);
    if ((this.snapshot.observation?.positionMs ?? 0) > 3000) this.seek(fence, 0);
    else await this.load(this.snapshot.index - 1);
  }
  async replay(fence: LocalPlaybackFence) {this.require(fence);await this.load(this.snapshot.index);}
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;++this.serial;this.life.abort();this.off();this.options.port.unload();
    this.publish({phase: 'disposed', fence: undefined, observation: undefined});this.listeners.clear();
    this.releaseTask ??= Promise.resolve().then(this.options.release).catch(this.options.reportError);
  }
}
