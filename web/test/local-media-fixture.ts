import type {PlayerEvent, PlayerObservation, PlayerSource} from '@core/playback-v1/port.ts';
import {localPlayerLoadReceipt, type LocalPlayerSource, type LocalPlayerLoadContext} from '@core/playback-v1/local-port.ts';
import type {PlaybackMediaPort} from '../src/platform/playback/web-playback.ts';
import type {WebLocalQueuePreparation} from '../src/platform/playback/local-queue.ts';

/** Controlled media-port boundary, not a native receipt/file/physical playback qualification. */
export class ControlledLocalMedia implements PlaybackMediaPort {
  contexts: LocalPlayerLoadContext[] = [];
  loaded = 0;settling = false;unloads = 0;urls: PlayerSource[] = [];loads: LocalPlayerSource[] = [];
  private serial = 0;
  private observation: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  private listeners = new Set<(event: PlayerEvent) => void>();
  gate?: (source: LocalPlayerSource) => Promise<void>;
  load(source: PlayerSource) {this.urls.push(source);this.loaded = source.generation;}
  async loadLocal(source: LocalPlayerSource, context: LocalPlayerLoadContext) {
    const sequence = ++this.serial;this.loads.push(source);this.contexts.push(context);
    await this.gate?.(source);
    const receipt = localPlayerLoadReceipt({kind: 'local', scope: source.scope, preparationId: source.preparationId, localSourceId: source.localSourceId,
      entryId: source.entryId, itemId: source.itemId, generation: source.generation,
      sessionId: `local:${source.entryId}:${sequence}`, playId: `journal_${sequence}`, artifact: {sha256: String(sequence % 10).repeat(64), bytes: sequence}}, source);
    if (!context.signal.aborted && context.isCurrent()) {
      this.loaded = source.generation;
      this.observation = {generation: source.generation, state: source.autoplay ? 'playing' : 'paused', positionMs: source.startPositionMs, rate: 1};
      this.emit();
    }
    return receipt;
  }
  observe() {return this.observation;}
  onEvent(listener: (event: PlayerEvent) => void) {this.listeners.add(listener);return () => {this.listeners.delete(listener);};}
  onNotice() {return () => {};}
  emit(value: Partial<PlayerObservation> = {}) {this.observation = {...this.observation, ...value};for (const listener of [...this.listeners]) listener({type: 'state', observation: this.observation});}
  play() {this.emit({state: 'playing'});}
  pause() {if (this.loaded) this.emit({state: 'paused'});}
  seek(positionMs: number) {this.emit({positionMs});}
  setRate(rate: number) {this.observation = {...this.observation, rate};}
  selectSidecarSubtitle() {}
  unload() {this.unloads++;this.loaded = 0;this.observation = {...this.observation, state: 'idle'};}
  dispose() {this.unload();this.listeners.clear();}
  suspend() {}
  resume() {}
}
export const localScope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', sessionFamilyId: 'local_family'};
export function localPreparation(): WebLocalQueuePreparation {
  return {preparationId: 'actual_preparation', scope: localScope, selectionRevision: 'aggregate_revision', items: [1, 2, 3].map((number, index) => ({
    id: `download_${number}`, revision: number, itemId: `song_${number}`, localSourceId: `opaque_${number}`, durationMs: 100000,
    title: {kind: 'song', title: `Stored song ${number}`, year: 2026, durationSeconds: 100, parentId: 'album', parentTitle: 'Stored album',
      groupId: 'disc', groupTitle: 'Disc 1', groupNumber: 1, number: index + 1, artist: 'Stored artist', onServer: false},
  }))};
}
