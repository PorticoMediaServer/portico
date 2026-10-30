import type {SelectionActionSource} from '@core/selection-actions-controller.ts';
import {listSelectionActionPreparations, type SelectionActionsApi} from '@core/selection-actions.ts';
import {parseDownloadGrant, parseDownloadPreparation, type DownloadPreparation} from '@core/downloads.ts';
import {routePath} from '../../../../packages/contracts/src/route-paths.ts';

export type BrowserSelectionActionApi = SelectionActionsApi & Readonly<{mediaUrl: (path: string) => string}>;
export type SelectionActionTransfers = Readonly<{
  phase: 'idle' | 'loading' | 'waiting' | 'ready' | 'handing-off' | 'complete' | 'error';
  /** One bounded inventory page, consumed in order. No full-selection member set. */
  items: readonly DownloadPreparation[]; nextCursor: string; closed: boolean; handedOff: number; errorCode: string | null;
}>;
const emptyTransfers = (): SelectionActionTransfers => Object.freeze({phase: 'idle', items: Object.freeze([]), nextCursor: '', closed: false, handedOff: 0, errorCode: null});
export type BrowserSelectionActionOptions = Readonly<{
  api: BrowserSelectionActionApi; requestId: () => Promise<string>;
  /** The browser acknowledges delivery of an attachment capability, never successful file storage. */
  handoff?: (url: string, filename: string) => void;
}>;
function attachment(url: string, filename: string) {
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = filename; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
  document.body.append(anchor); try {anchor.click();} finally {anchor.remove();}
}

/** A bounded, user-driven queue of actual preparations linked to one confirmed operation. */
export class SelectionActionTransferQueue {
  private transfers = emptyTransfers();
  private listeners = new Set<() => void>();
  private source?: SelectionActionSource;
  private lifetime?: AbortController;
  private releaseSource?: () => void;
  private operationId?: string;
  private deviceId?: string;
  private grant?: Readonly<{preparationId: string; operationId: string}>;
  private visitedCursor?: string;
  private stopped = false;
  constructor(private readonly options: BrowserSelectionActionOptions) {}
  getTransfers = (): SelectionActionTransfers => this.transfers;
  subscribeTransfers = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<SelectionActionTransfers>) {this.transfers = Object.freeze({...this.transfers, ...patch}); for (const listener of [...this.listeners]) listener();}
  private current() {return !this.stopped && !!this.lifetime && !this.lifetime.signal.aborted && !!this.source && !this.source.signal?.aborted && this.source.isCurrent();}
  private retireTransfers() {
    this.lifetime?.abort(); this.releaseSource?.(); this.releaseSource = undefined;
    this.source = undefined; this.lifetime = undefined; this.operationId = undefined; this.deviceId = undefined;
    this.grant = undefined; this.visitedCursor = undefined; this.transfers = emptyTransfers();
    for (const listener of [...this.listeners]) listener();
  }
  open(operationId: string, deviceId: string, source: SelectionActionSource): Promise<void> {
    if (!/^[A-Za-z0-9_-]{1,160}$/.test(operationId) || !/^[A-Za-z0-9_-]{1,160}$/.test(deviceId)) throw new Error('download-inventory-unavailable');
    this.retireTransfers();
    this.lifetime = new AbortController(); this.source = source; this.deviceId = deviceId; this.operationId = operationId;
    const retire = () => {if (!this.current()) this.retireTransfers();};
    const unsubscribe = source.subscribe(retire);
    source.signal?.addEventListener('abort', retire, {once: true});
    this.releaseSource = () => {unsubscribe(); source.signal?.removeEventListener('abort', retire);};
    if (!this.current()) {this.retireTransfers(); return Promise.resolve();}
    return this.loadPage('');
  }
  private async loadPage(cursor: string) {
    if (!this.current() || !this.operationId || !this.deviceId || this.transfers.phase === 'loading') return;
    const lifetime = this.lifetime!, operationId = this.operationId, deviceId = this.deviceId;
    this.visitedCursor = cursor;
    this.publish({phase: 'loading', errorCode: null});
    try {
      if (!this.current() || this.lifetime !== lifetime) return;
      let page = await listSelectionActionPreparations(this.options.api, operationId, deviceId, {cursor, limit: 20, signal: lifetime.signal});
      if (!this.current() || this.lifetime !== lifetime) return;
      const started = Date.now();
      while (!page.closed) {
        if (cursor || this.transfers.closed) throw new Error('download-inventory-reopened');
        // No fixed snapshot until closure: discard active items/cursor and poll the same exact header.
        this.publish({items: Object.freeze([]), nextCursor: '', closed: false, phase: 'waiting', errorCode: null});
        await new Promise<void>((resolve, reject) => {
          const abort = () => {clearTimeout(timer); lifetime.signal.removeEventListener('abort', abort); reject(new Error('cancelled'));};
          const timer = setTimeout(() => {lifetime.signal.removeEventListener('abort', abort); resolve();}, 1500);
          lifetime.signal.addEventListener('abort', abort, {once: true}); if (lifetime.signal.aborted) abort();
        });
        if (!this.current() || this.lifetime !== lifetime) return;
        if (Date.now() - started >= 30 * 60_000) throw new Error('download-inventory-still-active');
        page = await listSelectionActionPreparations(this.options.api, operationId, deviceId, {limit: 20, signal: lifetime.signal});
        if (!this.current() || this.lifetime !== lifetime) return;
      }
      if (page.nextCursor && page.nextCursor === cursor) throw new Error('download-inventory-cursor-repeated');
      this.publish({closed: true, items: page.items, nextCursor: page.nextCursor, phase: page.items.length ? 'ready' : page.nextCursor ? 'ready' : 'complete'});
    } catch (error) {if (this.current() && this.lifetime === lifetime) this.publish({phase: 'error', errorCode: String((error as {code?: unknown})?.code ?? 'download_inventory_failed')});}
  }
  async nextPage(): Promise<void> {
    if (this.transfers.items.length || !this.transfers.nextCursor || this.transfers.phase !== 'ready') return;
    return this.loadPage(this.transfers.nextCursor);
  }
  async retryInventory(): Promise<void> {
    if (this.transfers.phase === 'error' && !this.transfers.items.length) return this.loadPage(this.visitedCursor ?? '');
  }
  /** One visible user action hands off one real grant. Browsers may still require download permission. */
  async transferNext(): Promise<void> {
    let preparation = this.transfers.items[0]; const lifetime = this.lifetime;
    if (!this.current() || !lifetime || !this.transfers.closed || !preparation || !['ready', 'error'].includes(this.transfers.phase)) return;
    this.publish({phase: 'handing-off', errorCode: null});
    try {
      if (!this.current() || this.lifetime !== lifetime) return;
      const started = Date.now();
      while (preparation.state === 'queued' || preparation.state === 'running') {
        if (Date.now() - started > 300_000) throw new Error('download-preparation-pending');
        await new Promise<void>((resolve, reject) => {
          const abort = () => {clearTimeout(timer); lifetime.signal.removeEventListener('abort', abort); reject(new Error('cancelled'));};
          const timer = setTimeout(() => {lifetime.signal.removeEventListener('abort', abort); resolve();}, 1500);
          lifetime.signal.addEventListener('abort', abort, {once: true});
          if (lifetime.signal.aborted) abort();
        });
        if (!this.current() || this.lifetime !== lifetime) return;
        const refreshed = parseDownloadPreparation(await this.options.api.request(routePath('get_download_preparation', {id: preparation.id}), 'GET', undefined, lifetime.signal));
        if (!this.current() || this.lifetime !== lifetime) return;
        if (refreshed.id !== preparation.id || refreshed.itemId !== preparation.itemId || refreshed.libraryId !== preparation.libraryId || refreshed.quality !== preparation.quality) throw new Error('download-preparation-mismatch');
        preparation = refreshed;
        this.publish({items: Object.freeze([preparation, ...this.transfers.items.slice(1)])});
      }
      if (preparation.state !== 'ready') throw new Error('download-not-ready');
      if (!this.grant || this.grant.preparationId !== preparation.id) {
        const operationId = await this.options.requestId();
        if (!this.current() || this.lifetime !== lifetime) return;
        this.grant = Object.freeze({preparationId: preparation.id, operationId});
      }
      const grant = parseDownloadGrant(await this.options.api.request(routePath('create_download_grant', {id: preparation.id}), 'POST', {operationId: this.grant.operationId}, lifetime.signal));
      if (!this.current() || this.lifetime !== lifetime) return;
      if (grant.preparationId !== preparation.id || grant.itemId !== preparation.itemId || grant.artifact.sha256 !== preparation.artifact.sha256
        || grant.artifact.bytes !== preparation.artifact.bytes || Date.parse(grant.expiresAt) <= Date.now()) throw new Error('download-grant-mismatch');
      const href = this.options.api.mediaUrl(grant.url);
      if (!this.current() || this.lifetime !== lifetime) return;
      (this.options.handoff ?? attachment)(href, grant.artifact.fileName);
      this.grant = undefined;
      const remaining = Object.freeze(this.transfers.items.slice(1));
      this.publish({items: remaining, handedOff: this.transfers.handedOff + 1, phase: remaining.length || this.transfers.nextCursor ? 'ready' : 'complete'});
    } catch (error) {if (this.current() && this.lifetime === lifetime) this.publish({phase: 'error', errorCode: String((error as {code?: unknown})?.code ?? 'download_handoff_failed')});}
  }
  /** A failed preparation may be skipped locally; no server claim or confirmed command is changed. */
  skipNext() {
    if (!this.current() || !this.transfers.items.length || this.transfers.phase === 'handing-off') return;
    this.grant = undefined;
    const items = Object.freeze(this.transfers.items.slice(1));
    this.publish({items, phase: items.length || this.transfers.nextCursor ? 'ready' : 'complete', errorCode: null});
  }
  close() {this.retireTransfers();}
  dispose() {this.stopped = true; this.retireTransfers(); this.listeners.clear();}
}
