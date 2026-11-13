import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {nextOnNowSource, liveGuideChannel} from '../../../../../packages/client-core/src/guide/next-source';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import type {ChannelApi} from '../../../../../packages/client-core/src/channel-guide';

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
  /** When the first programme on now at `at` ends: the row is read again once the clock passes it, so a card never
   * shows a programme that has finished (or "No information" for the gap its end leaves in the read). */
  until?: number;
}>;
export type NativeSearchOnNowPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeSearchOnNowSnapshot;
  load: () => Promise<void>;
}>;
const LIMIT = 4;
const idle: NativeSearchOnNowSnapshot = Object.freeze({phase: 'idle', cards: Object.freeze([]), at: 0});

/** Up to four channels from the rebuilt server's favourites-first On now read. It never ranks or follows a continuation, and a refused
 * or failed Live TV read leaves the row absent (`hidden`) rather than showing an error. */
export class NativeSearchOnNow implements NativeSearchOnNowPort {
  private state = idle;
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  constructor(private readonly options: Readonly<{api: ChannelApi; serverId: string; /** The viewer's zone; the app's own region by default. */ timezone?: string; current: () => boolean;
    /** The On now read over `api` unless given (tests). */ source?: Pick<ReturnType<typeof nextOnNowSource>, 'channels' | 'programs'>}>) {}
  getSnapshot = (): NativeSearchOnNowSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  private publish(state: NativeSearchOnNowSnapshot) {this.state = Object.freeze(state); for (const listener of [...this.listeners]) listener();}
  /** Loads once, and again only once a programme it shows has ended (`until`): a loading row, or a ready one still
   * current, is not read again by a re-render. A row read again keeps its cards until the new read answers. */
  load = (): Promise<void> => {
    if (this.disposed || !this.options.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    const ready = this.state.phase === 'ready';
    if (ready && (this.state.until === undefined || serverNow() < this.state.until)) return Promise.resolve();
    const controller = new AbortController(); this.controller = controller;
    const live = () => !this.disposed && !controller.signal.aborted && this.options.current();
    if (!ready) this.publish({...idle, phase: 'loading'});
    const task = (async () => {
      try {
        const now = serverNow();
        const source = this.options.source ?? nextOnNowSource(this.options.api);
        const page = await source.channels(0, LIMIT, controller.signal);
        if (!live()) return;
        if (!page.items.length) {this.publish({...idle, phase: 'ready', at: now}); return;}
        const channels: readonly GuideChannelRow[] = page.items.slice(0, LIMIT);
        const programmes = await source.programs(channels.map(channel => channel.id), now, now + 1, controller.signal);
        if (!live()) return;
        const cards = Object.freeze(channels.map(channel => Object.freeze({
          channel: Object.freeze({sourceId: liveGuideChannel(channel)?.sourceId ?? channel.sourceId ?? '', id: channel.id, name: channel.name,
            ...(channel.number !== undefined ? {number: channel.number} : {}), ...(channel.mark ? {mark: channel.mark} : {}),
            ...(channel.logoUrl ? {logoUrl: channel.logoUrl} : {})}),
          programmes: Object.freeze(programmes[channel.id] ?? []),
        })));
        const ends = cards.flatMap(card => card.programmes.filter(programme => programme.start <= now && programme.end > now).map(programme => programme.end));
        this.publish({phase: 'ready', cards, at: now, ...(ends.length ? {until: Math.min(...ends)} : {})});
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
