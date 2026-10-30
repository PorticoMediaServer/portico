import {readLibrarySummaries, type LibrarySummary} from '../../../../../packages/client-core/src/library-summary';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';

export type NativeLibraryDirectorySnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  libraries: readonly Readonly<{id: string; name: string; kind: string; summary: LibrarySummary}>[];
  error?: unknown;
}>;
/** The existing bounded, authenticated library summary producer. No catalogue
 * reads, administration counts or inferred totals back the library switcher. */
export class NativeLibraryDirectory {
  private state: NativeLibraryDirectorySnapshot = Object.freeze({phase: 'idle', libraries: []});
  private controller?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  private readonly listeners = new Set<() => void>();
  private readonly options: Readonly<{api: LibraryContentApi; current: () => boolean}>;
  constructor(options: NativeLibraryDirectory['options']) {this.options = options;}
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = () => {this.current(); return this.state;};
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(state: NativeLibraryDirectorySnapshot) {
    if (!this.current()) return;
    this.state = Object.freeze(state); for (const listener of [...this.listeners]) listener();
  }
  load = (refresh = false): Promise<void> => {
    if (!this.current() || !refresh && this.state.phase === 'ready') return Promise.resolve();
    if (this.pending) return this.pending;
    const controller = new AbortController(); this.controller = controller; this.publish({phase: 'loading', libraries: this.state.libraries});
    const task = readLibrarySummaries(this.options.api, controller.signal).then(libraries => {
      if (!controller.signal.aborted) this.publish({phase: 'ready', libraries});
    }).catch(error => {if (!controller.signal.aborted) this.publish({phase: 'error', libraries: [], error});})
      .finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  dispose = () => {if (this.disposed) return; this.disposed = true; this.controller?.abort(); this.state = Object.freeze({phase: 'idle', libraries: []}); this.listeners.clear();};
}
