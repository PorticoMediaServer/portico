import type {VersionDeletion} from '@core/administration.ts';

/** What the confirm offers for a preview: the server's own refusal up front with nothing to type or press when the
 * delete would be refused (deleting files is turned off for this library), else the title to type and Delete. */
export function versionDeletionConfirm(preview: VersionDeletion): Readonly<{refusal?: string; asksTitle: boolean; offersDelete: boolean}> {
  return preview.deleteRefusal ? {refusal: preview.deleteRefusal, asksTitle: false, offersDelete: false} : {asksTitle: true, offersDelete: true};
}

export type VersionDeletionState = Readonly<{phase: 'checking'} | {phase: 'ready'; preview: VersionDeletion} | {phase: 'failed'; error: unknown}>;

/** One press of Delete this version…. The server's preview runs the real deletion as a dry run on its single writer and
 * can hold it for about a second, so it is asked here, once, when the owner presses Delete, and again only when they
 * press Try again after a failure. It is started by the press itself, outside React, so no render, effect, focus,
 * hover or timer can ask it again; the confirm only reads it. Closing it stops a preview still on its way. */
export class VersionDeletionOpening {
  private state: VersionDeletionState = Object.freeze({phase: 'checking'});
  private controller?: AbortController;
  private readonly listeners = new Set<() => void>();
  private closed = false;

  constructor(private readonly preview: (signal: AbortSignal) => Promise<VersionDeletion>, private readonly report: (error: unknown) => void = () => {}) {
    this.ask();
  }
  getSnapshot = (): VersionDeletionState => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  /** Try again after a failed preview: the owner's own press, so one more request. */
  retry = (): void => { if (!this.closed && this.state.phase === 'failed') this.ask(); };
  dispose = (): void => { this.closed = true; this.controller?.abort(); this.listeners.clear(); };

  private publish(state: VersionDeletionState): void {
    if (this.closed) return;
    this.state = Object.freeze(state);
    for (const listener of [...this.listeners]) listener();
  }
  private ask(): void {
    const controller = this.controller = new AbortController();
    this.publish({phase: 'checking'});
    void this.preview(controller.signal).then(preview => {
      if (!controller.signal.aborted) this.publish({phase: 'ready', preview});
    }, error => {
      if (controller.signal.aborted) return;
      this.report(error);
      this.publish({phase: 'failed', error: error ?? true});
    });
  }
}
