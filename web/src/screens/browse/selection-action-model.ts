import {SelectionActionController, type SelectionActionSource} from '@core/selection-actions-controller.ts';
import {selectionActionTargetInput, type SelectionActionOptions} from '@core/selection-actions.ts';
import type {QuerySelectionTarget} from '@core/selection-model/types.ts';
import {SelectionActionTransferQueue, type BrowserSelectionActionOptions} from './selection-action-transfers.ts';
export type {BrowserSelectionActionOptions, BrowserSelectionActionApi, SelectionActionTransfers} from './selection-action-transfers.ts';

/** One canonical controller dispatches Delete AND Download. File handoff is a separate bounded sink. */
export class BrowserSelectionActionModel {
  readonly controller: SelectionActionController;
  private readonly transfers: SelectionActionTransferQueue;
  private source?: SelectionActionSource;
  private deviceId?: string;
  private followed?: string;
  private unsubscribe: () => void;
  constructor(options: BrowserSelectionActionOptions) {
    this.controller = new SelectionActionController(options); this.transfers = new SelectionActionTransferQueue(options);
    this.unsubscribe = this.controller.subscribe(() => {
      const state = this.controller.getSnapshot(), job = state.job;
      if (state.phase === 'idle' || state.phase === 'retired') {this.source = undefined; this.deviceId = undefined; this.followed = undefined; this.transfers.close();}
      // The durable header, not the short-lived job summary, proves closure for the fixed cursor drain.
      if (job?.action === 'download' && this.followed !== job.operationId && this.source && this.deviceId) {
        this.followed = job.operationId; void this.transfers.open(job.operationId, this.deviceId, this.source);
      }
    });
  }
  getTransfers = () => this.transfers.getTransfers();
  subscribeTransfers = (listener: () => void) => this.transfers.subscribeTransfers(listener);
  open<C>(target: QuerySelectionTarget<C>, action: SelectionActionOptions, source: SelectionActionSource): Promise<void> {
    const input = selectionActionTargetInput(target, action);
    this.close();
    // open publishes its retirement/preparing state synchronously before its first transport await.
    const pending = this.controller.open(input, source);
    if (source.isCurrent() && !source.signal?.aborted) {this.source = source;this.deviceId = input.action === 'download' ? input.options.deviceId : undefined;}
    return pending;
  }
  nextPage = () => this.transfers.nextPage();
  retryInventory = () => this.transfers.retryInventory();
  transferNext = () => this.transfers.transferNext();
  skipNext = () => this.transfers.skipNext();
  close() {this.controller.close(); this.transfers.close(); this.source = undefined; this.deviceId = undefined; this.followed = undefined;}
  dispose() {this.unsubscribe(); this.controller.dispose(); this.transfers.dispose(); this.source = undefined; this.deviceId = undefined;}
}
