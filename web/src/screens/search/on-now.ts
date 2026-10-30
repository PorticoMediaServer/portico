import {legacyGuideSource, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {fetchGuideSourceSummaries} from '@core/guide/directory.ts';
import type {ChannelApi} from '@core/channel-guide.ts';

export type SearchOnNowCard = Readonly<{
  channel: Readonly<{sourceId: string; id: string; name: string; number?: string; mark?: Readonly<{text: string; seed: string}>; logoUrl?: string}>;
  programmes: readonly GuideProgram[];
}>;
export type SearchOnNowSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'hidden';
  cards: readonly SearchOnNowCard[];
  sourceNames: readonly string[];
  error?: unknown;
}>;
const empty = (): SearchOnNowSnapshot => Object.freeze({phase: 'idle', cards: Object.freeze([]), sourceNames: Object.freeze([])});
const cancelled = () => new DOMException('Search Live TV changed', 'AbortError');

/** Four drawn channels from one bounded directory page and one programme window.
 * Favourite order is the server's existing directory order; this class never
 * ranks, filters by permission, or follows a continuation. A refused Live TV
 * read leaves the section absent instead of exposing an error or another source. */
export class SearchOnNowController {
  private api: ChannelApi;
  private serverId: string;
  private timezone: string;
  private current: () => boolean;
  private signal: AbortSignal;
  private state = empty();
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  private limit: number;
  constructor(input: Readonly<{api: ChannelApi; serverId: string; timezone: string; isCurrent: () => boolean; signal: AbortSignal; limit?: number}>) {
    if (!input.serverId || !input.timezone) throw new Error('Invalid Search Live TV scope');
    const limit = input.limit ?? 4;
    if (!Number.isSafeInteger(limit) || limit < 1 || limit > 4) throw new Error('Invalid Search Live TV window');
    this.api = input.api;this.serverId = input.serverId;this.timezone = input.timezone;this.current = input.isCurrent;this.signal = input.signal;
    this.limit = limit;
    this.signal.addEventListener('abort', this.dispose, {once: true});
    if (this.signal.aborted) this.dispose();
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);return () => {this.listeners.delete(listener);};
  };
  private active() {return !this.disposed && !this.signal.aborted && this.current();}
  private publish(state: SearchOnNowSnapshot) {
    this.state = Object.freeze(state);for (const listener of this.listeners) listener();
  }
  /** One load owns both reads; a later load replaces it only after aborting the old one. */
  load = (): Promise<void> => {
    this.cancel();
    if (!this.active()) return Promise.reject(cancelled());
    return this.read();
  };
  private read(): Promise<void> {
    const generation = this.generation, controller = new AbortController(), signal = AbortSignal.any([controller.signal, this.signal]);
    this.controller = controller;
    this.publish({phase: 'loading', cards: Object.freeze([]), sourceNames: Object.freeze([])});
    const task = (async () => {
      try {
        const now = Date.now();
        const source = legacyGuideSource(this.api, this.serverId, {kind: 'all', sourceId: '', timezone: this.timezone, sort: 'favorites'});
        const page = await source.channels(0, this.limit, signal);
        if (!this.active() || generation !== this.generation || signal.aborted) throw cancelled();
        if (!page.items.length) {this.publish({phase: 'ready', cards: Object.freeze([]), sourceNames: Object.freeze([])});return;}
        const channels: readonly GuideChannelRow[] = page.items.slice(0, this.limit);
        const [programmes, summaries] = await Promise.all([
          source.programs(channels.map(channel => channel.id), now, now + 1, signal),
          fetchGuideSourceSummaries(this.api, this.serverId, this.timezone, signal).catch(() => undefined),
        ]);
        if (!this.active() || generation !== this.generation || signal.aborted) throw cancelled();
        const cards = Object.freeze(channels.map(channel => Object.freeze({
          channel: Object.freeze({sourceId: channel.sourceId ?? '', id: channel.id, name: channel.name,
            ...(channel.number !== undefined ? {number: channel.number} : {}), ...(channel.mark ? {mark: channel.mark} : {}),
            ...(channel.logoUrl ? {logoUrl: channel.logoUrl} : {})}),
          programmes: Object.freeze(programmes[channel.id] ?? []),
        })));
        const sourceNames = Object.freeze([...new Set(channels.flatMap(channel => {
          const name = summaries?.sources.find(candidate => candidate.id === channel.sourceId)?.name;
          return name ? [name] : [];
        }))]);
        this.publish({phase: 'ready', cards, sourceNames});
      } catch (error) {
        if (generation !== this.generation) throw error;
        this.publish({phase: this.active() ? 'hidden' : 'idle', cards: Object.freeze([]), sourceNames: Object.freeze([]), ...(this.active() ? {error} : {})});
        throw error;
      } finally {
        if (generation === this.generation) {this.controller = undefined;this.pending = undefined;}
      }
    })();
    this.pending = task;return task;
  }
  cancel = () => {
    this.generation++;this.controller?.abort();this.controller = undefined;this.pending = undefined;
    if (this.state.phase !== 'idle') this.publish(empty());
  };
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true;this.signal.removeEventListener('abort', this.dispose);this.cancel();this.listeners.clear();
  };
}
