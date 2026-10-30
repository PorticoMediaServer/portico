import {clearPlaybackFailureMemory, groupPlaybackFailures, readPlaybackFailureMemory, type PlaybackFailureGroup} from '@core/playback-failure-memory.ts';
import type {ReportTransport} from '@core/client-reports.ts';

type Snapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; groups: readonly PlaybackFailureGroup[]; more: boolean; busy?: string; error?: unknown}>;
const empty: Snapshot = Object.freeze({phase: 'idle', groups: [], more: false});
export type PlaybackProblemsOptions = Readonly<{transport: ReportTransport; isCurrent: () => boolean; subscribe: (listener: () => void) => () => void; reportError: (error: unknown) => void}>;

/** Owner transport/currentness are captured by Settings. This leaf never changes
 * remembered failure policy; Forget uses the existing exact file command. */
export class PlaybackProblemsSource {
  private state: Snapshot = empty;
  private listeners = new Set<() => void>();
  private lifetime = new AbortController();
  private read?: AbortController;
  private disposed = false;
  private off: () => void;
  constructor(private options: PlaybackProblemsOptions) {
    this.off = options.subscribe(() => {if (!options.isCurrent()) this.retire();});
  }
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private current() {return !this.disposed && !this.lifetime.signal.aborted && this.options.isCurrent();}
  private publish(state: Snapshot) {this.state = Object.freeze(state);for (const listener of this.listeners) listener();}
  private retire() {this.lifetime.abort();this.read?.abort();this.publish(empty);}
  async load() {
    if (!this.current()) return;
    this.read?.abort();
    const request = this.read = new AbortController();
    this.publish({...this.state, phase: 'loading', error: undefined});
    if (!this.current() || request.signal.aborted) return;
    try {
      const value = await readPlaybackFailureMemory(this.options.transport, request.signal);
      if (this.current() && !request.signal.aborted) this.publish({...this.state, phase: 'ready', groups: groupPlaybackFailures(value.items), more: value.more});
    } catch (error) {
      if (this.current() && !request.signal.aborted) {this.publish({...this.state, phase: 'error', groups: [], more: false, error});this.options.reportError(error);}
    }
  }
  async forget(sourceId: string) {
    if (!this.current() || this.state.busy || !this.state.groups.some(group => group.sourceId === sourceId)) return;
    this.publish({...this.state, busy: sourceId, error: undefined});
    if (!this.current()) return;
    try {
      await clearPlaybackFailureMemory(this.options.transport, sourceId, this.lifetime.signal);
      if (this.current()) await this.load();
    } catch (error) {
      if (this.current()) {this.publish({...this.state, error});this.options.reportError(error);}
    } finally {
      if (this.current()) this.publish({...this.state, busy: undefined});
    }
  }
  dispose() {this.disposed = true;this.off();this.retire();this.listeners.clear();}
}
