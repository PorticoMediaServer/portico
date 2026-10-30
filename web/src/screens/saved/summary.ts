import {fetchSavedSummary, type SavedApi, type SavedLandingSummary, type SavedScope} from '@core/saved.ts';
import type {SavedOperations} from './operations.ts';

export type SavedSummarySnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; value?: SavedLandingSummary; error?: unknown}>;
const empty: SavedSummarySnapshot = Object.freeze({phase: 'idle'});

/** Captured view state for the existing canonical summary, never page totals. */
export class SavedSummary {
  private snapshot = empty;
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private closed = false;

  constructor(private readonly api: SavedApi, private readonly scope: SavedScope, private readonly operations: SavedOperations) {}
  getSnapshot = (): SavedSummarySnapshot => this.closed || !this.operations.current ? empty : this.snapshot;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: SavedSummarySnapshot): void {
    if (this.closed || !this.operations.current) return;
    this.snapshot = Object.freeze(value); for (const listener of this.listeners) listener();
  }
  async refresh(signal?: AbortSignal): Promise<void> {
    if (this.closed || !this.operations.current || signal?.aborted) return;
    this.controller?.abort(); const controller = new AbortController(); this.controller = controller;
    const stop = () => controller.abort(); signal?.addEventListener('abort', stop, {once: true});
    this.publish({...this.snapshot, phase: this.snapshot.value ? 'ready' : 'loading', error: undefined});
    try {
      const value = await fetchSavedSummary(this.api, this.scope, controller.signal);
      if (!controller.signal.aborted) this.publish({phase: 'ready', value});
    } catch (error) {
      if (!controller.signal.aborted) this.publish({...this.snapshot, phase: 'error', error});
    } finally { signal?.removeEventListener('abort', stop); }
  }
  dispose(): void {
    this.closed = true; this.controller?.abort(); this.snapshot = empty;
    for (const listener of this.listeners) listener(); this.listeners.clear();
  }
}
