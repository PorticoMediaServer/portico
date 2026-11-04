import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {legacyGuideSource} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import type {ChannelApi} from '../../../../../packages/client-core/src/channel-guide';
import {defaultI18n} from '../../../../../packages/i18n/src';

/** phone-app.html #8 "On now · Guide": the empty Search page's live row. */
export type NativeSearchOnNowCard = Readonly<{
  channel: Readonly<{sourceId: string; id: string; name: string; number?: string; mark?: Readonly<{text: string; seed: string}>; logoUrl?: string}>;
  programmes: readonly GuideProgram[];
}>;
export type NativeSearchOnNowSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'hidden';
  cards: readonly NativeSearchOnNowCard[];
  /** The server's own time the programmes were read for. */
  at: number;
}>;
export type NativeSearchOnNowPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeSearchOnNowSnapshot;
  load: () => Promise<void>;
}>;
const LIMIT = 4;
const idle: NativeSearchOnNowSnapshot = Object.freeze({phase: 'idle', cards: Object.freeze([]), at: 0});

/** Up to four channels from one favourites-ordered directory page and one programme window, the
 * same bounded read web's Search makes. It never ranks or follows a continuation, and a refused
 * or failed Live TV read leaves the row absent (`hidden`) rather than showing an error. */
export class NativeSearchOnNow implements NativeSearchOnNowPort {
  private state = idle;
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  constructor(private readonly options: Readonly<{api: ChannelApi; serverId: string; /** The viewer's zone; the app's own region by default. */ timezone?: string; current: () => boolean}>) {}
  getSnapshot = (): NativeSearchOnNowSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  private publish(state: NativeSearchOnNowSnapshot) {this.state = Object.freeze(state); for (const listener of [...this.listeners]) listener();}
  /** Loads once; a ready or loading row is not read again by a re-render. */
  load = (): Promise<void> => {
    if (this.disposed || !this.options.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    if (this.state.phase === 'ready') return Promise.resolve();
    const controller = new AbortController(); this.controller = controller;
    const live = () => !this.disposed && !controller.signal.aborted && this.options.current();
    this.publish({...idle, phase: 'loading'});
    const task = (async () => {
      try {
        const now = serverNow(), timezone = this.options.timezone ?? defaultI18n.region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone;
        const source = legacyGuideSource(this.options.api, this.options.serverId, {kind: 'all', sourceId: '', timezone, sort: 'favorites'});
        const page = await source.channels(0, LIMIT, controller.signal);
        if (!live()) return;
        if (!page.items.length) {this.publish({...idle, phase: 'ready', at: now}); return;}
        const channels: readonly GuideChannelRow[] = page.items.slice(0, LIMIT);
        const programmes = await source.programs(channels.map(channel => channel.id), now, now + 1, controller.signal);
        if (!live()) return;
        const cards = Object.freeze(channels.map(channel => Object.freeze({
          channel: Object.freeze({sourceId: channel.sourceId ?? '', id: channel.id, name: channel.name,
            ...(channel.number !== undefined ? {number: channel.number} : {}), ...(channel.mark ? {mark: channel.mark} : {}),
            ...(channel.logoUrl ? {logoUrl: channel.logoUrl} : {})}),
          programmes: Object.freeze(programmes[channel.id] ?? []),
        })));
        this.publish({phase: 'ready', cards, at: now});
      } catch {
        if (live()) this.publish({...idle, phase: 'hidden'});
      } finally {
        if (this.controller === controller) {this.controller = undefined; this.pending = undefined;}
      }
    })();
    this.pending = task; return task;
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.controller?.abort(); this.controller = undefined; this.pending = undefined; this.listeners.clear();
  };
}
