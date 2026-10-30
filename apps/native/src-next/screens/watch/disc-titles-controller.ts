import type {DetailApi} from '../../../../../packages/client-core/src/detail';
import {chooseDiscMainTitle, readDiscTitles, type DiscTitles} from './disc-titles';

/** One disc-title read or write, alive only while its opening, its physical
 * version and the owner session are all still the ones it was opened under. */
export type DiscTitlesLifetime = Readonly<{opening: () => boolean; owner: () => boolean; version: () => string | undefined}>;

export type NativeDiscTitlesState = Readonly<{
  phase: 'loading' | 'ready' | 'failed' | 'writing';
  titles?: DiscTitles;
  /** The write's own outcome, kept apart from the loaded facts. */
  error?: unknown;
  writingTitleId?: string;
}>;

/** The Titles sheet's facts. A read never turns a stored title into an
 * alternate version: this array is the scan's own published titles. */
export class NativeDiscTitlesController {
  private listeners = new Set<() => void>();
  private state: NativeDiscTitlesState = Object.freeze({phase: 'loading'});
  private disposed = false;
  private request?: AbortController;
  constructor(private api: DetailApi, private target: Readonly<{itemId: string; versionId: string}>, private lifetime: DiscTitlesLifetime) {}

  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = (): NativeDiscTitlesState => this.state;
  private publish(next: NativeDiscTitlesState): void {
    if (this.disposed) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  private current = (signal: AbortSignal): boolean => !this.disposed && !signal.aborted
    && this.lifetime.opening() && this.lifetime.owner() && this.lifetime.version() === this.target.versionId;
  private flight(): AbortSignal {
    this.request?.abort();
    const controller = new AbortController();
    this.request = controller;
    return controller.signal;
  }

  load = async (): Promise<void> => {
    // A public read never aborts a write in flight; the write's own refresh reads once.
    if (this.disposed || this.state.phase === 'writing') return;
    // One read at a time: a second call while the first is still loading neither
    // aborts it nor redraws it. The first read of an opening is the sheet's own.
    if (this.request && !this.request.signal.aborted && this.state.phase === 'loading') return;
    const signal = this.flight(), current = () => this.current(signal);
    if (!current()) return;
    this.publish({phase: 'loading'});
    try {
      const titles = await readDiscTitles(this.api, this.target.itemId, this.target.versionId, {signal, isCurrent: current});
      if (!titles || !current()) return;
      this.publish({phase: 'ready', titles});
    } catch (error) {
      if (current()) this.publish({phase: 'failed', error});
    }
  };

  /** The row that was drawn names its own document, and the write carries the
   * revision that row was drawn under. A refused revision re-reads once so the
   * sheet shows what the server has now; the refused write itself is never retried. */
  choose = async (titleId: string, drawn: DiscTitles): Promise<void> => {
    const loaded = this.state.titles;
    if (this.disposed || !loaded || loaded !== drawn || this.state.phase === 'writing') return;
    if (!drawn.titles.some(title => title.id === titleId) || titleId === (drawn.titles.find(title => title.main)?.id)) return;
    const signal = this.flight(), current = () => this.current(signal);
    if (!current()) return;
    this.publish({...this.state, phase: 'writing', writingTitleId: titleId, error: undefined});
    try {
      const titles = await chooseDiscMainTitle(this.api, {itemId: this.target.itemId, versionId: this.target.versionId, revision: drawn.revision, titleId}, {signal, isCurrent: current});
      if (!titles || !current()) return;
      this.publish({phase: 'ready', titles});
    } catch (error) {
      if (!current()) return;
      const code = (error as {code?: unknown} | null)?.code;
      if (code === 'disc_titles_stale') {this.publish({phase: 'ready', titles: drawn}); await this.load(); return;}
      this.publish({...this.state, phase: 'ready', writingTitleId: undefined, error});
    }
  };

  dispose = (): void => {if (this.disposed) return; this.disposed = true; this.request?.abort(); this.request = undefined; this.listeners.clear();};
}
