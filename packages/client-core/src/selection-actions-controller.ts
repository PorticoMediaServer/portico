import {
  cancelSelectionAction, createSelectionAction, getSelectionAction, getSelectionActionPreparation,
  isSelectionActionTerminal, prepareSelectionAction, releaseSelectionActionPreparation, selectionActionInput,
  type SelectionActionConfirmation, type SelectionActionInput, type SelectionActionJob,
  type SelectionActionPreparation, type SelectionActionsApi,
} from './selection-actions.ts';

export type SelectionActionSource = Readonly<{
  isCurrent: () => boolean;
  /** Exact count from the held model descriptor, when the caller has a model target. */
  expectedTotal?: number;
  /** Notify model operation, route, principal, authority or source replacement synchronously. */
  subscribe: (changed: () => void) => () => void;
  signal?: AbortSignal;
}>;
export type SelectionActionPhase = 'idle' | 'preparing' | 'ready' | 'confirming' | 'submitting' | 'running' | 'cancelling' | 'uncertain' | 'result' | 'error' | 'retired';
export type SelectionActionSnapshot = Readonly<{
  phase: SelectionActionPhase; input: SelectionActionInput | null; preparation: SelectionActionPreparation | null;
  confirmation: string; job: SelectionActionJob | null; errorCode: string | null;
}>;
export type SelectionActionControllerOptions = Readonly<{
  api: SelectionActionsApi; requestId: () => Promise<string>; now?: () => number;
}>;
type Ticket = {
  input: SelectionActionInput; source: SelectionActionSource; io: AbortController;
  expectedTotal?: number;
  cleanup: () => void; preparation?: SelectionActionPreparation; body?: SelectionActionConfirmation;
  job?: SelectionActionJob; cancelRequested: boolean; promise?: Promise<void>;
};
const empty = (phase: SelectionActionPhase = 'idle'): SelectionActionSnapshot => Object.freeze({phase, input: null, preparation: null, confirmation: '', job: null, errorCode: null});
const code = (error: unknown) => String((error as {code?: unknown})?.code ?? 'selection_action_unavailable');
const definite = (error: unknown) => [400, 401, 403, 404, 409, 422].includes(Number((error as {status?: unknown})?.status));
const authorityLost = (error: unknown) => [401, 403, 404].includes(Number((error as {status?: unknown})?.status));
const cancelled = () => Object.assign(new Error('Selection retired'), {code: 'cancelled'});
const timeoutMs = 30 * 60_000;
const delayMs = (value: number) => Math.min(timeoutMs, Math.max(250, value));

/** One exact whole-query action. Retains a bounded preview and aggregate progress, never target IDs. */
export class SelectionActionController {
  private state = empty();
  private listeners = new Set<() => void>();
  private ticket?: Ticket;
  private disposed = false;
  private now: () => number;
  constructor(private readonly options: SelectionActionControllerOptions) {this.now = options.now ?? Date.now;}
  getSnapshot = (): SelectionActionSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<SelectionActionSnapshot>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private live(ticket: Ticket, io?: AbortController): boolean {
    return !this.disposed && this.ticket === ticket && !ticket.source.signal?.aborted && ticket.source.isCurrent()
      && (!io || ticket.io === io && !io.signal.aborted);
  }
  private check(ticket: Ticket, io: AbortController) {
    if (!this.live(ticket, io)) {if (this.ticket === ticket && !this.live(ticket)) this.retire('retired'); throw cancelled();}
  }
  private release(ticket: Ticket) {
    if (ticket.body || !ticket.preparation) return;
    void releaseSelectionActionPreparation(this.options.api, ticket.input.selector.query.libraryId, ticket.preparation.preparationId).catch(() => {});
  }
  private retire(phase: 'idle' | 'retired') {
    const ticket = this.ticket; this.ticket = undefined;
    if (ticket) {ticket.io.abort(); ticket.cleanup(); this.release(ticket);}
    this.publish(empty(phase));
  }
  /** Local detachment never implicitly cancels an accepted server operation. */
  close() {this.retire('idle');}
  private async wait(ticket: Ticket, io: AbortController, milliseconds: number) {
    this.check(ticket, io);
    await new Promise<void>((resolve, reject) => {
      const abort = () => {clearTimeout(timer); io.signal.removeEventListener('abort', abort); reject(cancelled());};
      const timer = setTimeout(() => {io.signal.removeEventListener('abort', abort); resolve();}, delayMs(milliseconds));
      io.signal.addEventListener('abort', abort, {once: true});
      if (io.signal.aborted) abort();
    });
    this.check(ticket, io);
  }
  async open(input: SelectionActionInput, source: SelectionActionSource): Promise<void> {
    if (this.disposed) throw cancelled();
    const checked = selectionActionInput(input);
    if (source.expectedTotal !== undefined && (!Number.isSafeInteger(source.expectedTotal) || source.expectedTotal < 1)) throw Object.assign(new Error('Invalid selection count'), {code: 'invalid_selection_action'});
    this.retire('idle');
    if (source.signal?.aborted || !source.isCurrent()) {this.publish(empty('retired')); return;}
    const ticket: Ticket = {input: checked, source, expectedTotal: source.expectedTotal, io: new AbortController(), cleanup: () => {}, cancelRequested: false};
    this.ticket = ticket;
    const changed = () => {if (this.ticket === ticket && !this.live(ticket)) this.retire('retired');};
    const unsubscribe = source.subscribe(changed);
    source.signal?.addEventListener('abort', changed, {once: true});
    ticket.cleanup = () => {unsubscribe(); source.signal?.removeEventListener('abort', changed);};
    // subscribe itself may have delivered a synchronous retirement.
    if (!this.live(ticket)) {ticket.cleanup(); if (this.ticket === ticket) this.retire('retired'); return;}
    this.publish({...empty(), phase: 'preparing', input: checked});
    await this.load(ticket);
  }
  private async load(ticket: Ticket) {
    const io = ticket.io, started = this.now();
    try {
      this.check(ticket, io);
      let preparation = ticket.preparation
        ? await getSelectionActionPreparation(this.options.api, ticket.input.selector.query.libraryId, ticket.preparation.preparationId, io.signal)
        : await prepareSelectionAction(this.options.api, ticket.input, io.signal);
      // A server may finish a preparation after aborting the transport; release that late demand.
      if (!this.live(ticket, io)) {ticket.preparation = preparation; this.release(ticket); return;}
      for (;;) {
        this.check(ticket, io); ticket.preparation = preparation;
        if (preparation.kind === 'selection-action-preview') {
          if (preparation.action !== ticket.input.action || preparation.basis !== ticket.input.selector.query.selectionBasis
            || ticket.expectedTotal !== undefined && preparation.total !== ticket.expectedTotal
            || Date.parse(preparation.expiresAt) <= this.now()) throw Object.assign(new Error('Selection expired'), {code: 'stale_continuation'});
          this.publish({phase: 'ready', preparation, errorCode: null}); return;
        }
        this.publish({preparation});
        if (this.now() - started >= timeoutMs) throw Object.assign(new Error('Preparation timed out'), {code: 'selection_action_timeout'});
        await this.wait(ticket, io, Math.min(preparation.retryAfterMs, timeoutMs - (this.now() - started)));
        if (this.now() - started >= timeoutMs) throw Object.assign(new Error('Preparation timed out'), {code: 'selection_action_timeout'});
        preparation = await getSelectionActionPreparation(this.options.api, ticket.input.selector.query.libraryId, preparation.preparationId, io.signal);
      }
    } catch (error) {if (this.live(ticket, io)) {if (authorityLost(error)) this.retire('retired'); else this.publish({phase: 'error', errorCode: code(error)});}}
  }
  setConfirmation(confirmation: string) {
    const ticket = this.ticket;
    if (!ticket || !this.live(ticket) || this.state.phase !== 'ready') return;
    this.publish({confirmation});
  }
  async confirm(): Promise<void> {
    const ticket = this.ticket, preview = ticket?.preparation;
    if (!ticket || !this.live(ticket) || this.state.phase !== 'ready' || preview?.kind !== 'selection-action-preview') return;
    if (this.state.confirmation !== preview.confirmation) {this.publish({errorCode: 'confirmation_required'}); return;}
    if (Date.parse(preview.expiresAt) <= this.now()) {this.publish({phase: 'error', errorCode: 'stale_continuation'}); return;}
    this.publish({phase: 'confirming', errorCode: null});
    const io = ticket.io;
    try {
      const operationId = await this.options.requestId(); this.check(ticket, io);
      if (!/^[A-Za-z0-9_-]{1,160}$/.test(operationId)) throw Object.assign(new Error('Invalid operation id'), {code: 'invalid_operation_id'});
      // The first send owns this immutable body. No retry refreshes the selection or checks expiry.
      ticket.body = Object.freeze({operationId, preparationId: preview.preparationId, confirmation: preview.confirmation});
    } catch (error) {if (this.live(ticket, io)) this.publish({phase: 'ready', errorCode: code(error)}); return;}
    return this.run(ticket);
  }
  private accept(ticket: Ticket, job: SelectionActionJob) {
    const preview = ticket.preparation, previous = ticket.job;
    if (!ticket.body || preview?.kind !== 'selection-action-preview' || job.operationId !== ticket.body.operationId
      || job.action !== ticket.input.action || job.total !== preview.total
      || previous && (job.admitted < previous.admitted || job.done < previous.done || job.failed < previous.failed
        || job.bytesDeleted < previous.bytesDeleted || job.filesDeleted < previous.filesDeleted)) {
      throw Object.assign(new Error('Action response mismatch'), {code: 'invalid_selection_action'});
    }
    ticket.job = job;
    this.publish({job, phase: isSelectionActionTerminal(job.state) ? 'result' : ticket.cancelRequested ? 'cancelling' : 'running', errorCode: null});
  }
  private run(ticket: Ticket): Promise<void> {
    if (ticket.promise) return ticket.promise;
    const io = ticket.io;
    const promise = this.execute(ticket, io).finally(() => {if (ticket.promise === promise) ticket.promise = undefined;});
    ticket.promise = promise; return promise;
  }
  private async execute(ticket: Ticket, io: AbortController) {
    const started = this.now();
    try {
      this.check(ticket, io);
      if (!ticket.body) throw cancelled();
      if (!ticket.job) {
        this.publish({phase: 'submitting', errorCode: null});
        this.check(ticket, io);
        const job = await createSelectionAction(this.options.api, ticket.input.selector.query.libraryId, ticket.body, io.signal);
        this.check(ticket, io); this.accept(ticket, job);
      }
      if (ticket.cancelRequested && !isSelectionActionTerminal(ticket.job!.state)) {
        this.publish({phase: 'cancelling', errorCode: null});
        this.check(ticket, io);
        const job = await cancelSelectionAction(this.options.api, ticket.body.operationId, io.signal);
        this.check(ticket, io); this.accept(ticket, job);
      }
      while (!isSelectionActionTerminal(ticket.job!.state)) {
        if (this.now() - started >= timeoutMs) throw Object.assign(new Error('Action is still running'), {code: 'selection_action_timeout'});
        await this.wait(ticket, io, Math.min(ticket.job!.retryAfterMs, timeoutMs - (this.now() - started)));
        if (this.now() - started >= timeoutMs) throw Object.assign(new Error('Action is still running'), {code: 'selection_action_timeout'});
        const job = await getSelectionAction(this.options.api, ticket.body.operationId, io.signal);
        this.check(ticket, io); this.accept(ticket, job);
      }
    } catch (error) {
      if (this.live(ticket, io)) {if (authorityLost(error)) this.retire('retired'); else this.publish({phase: definite(error) ? 'error' : 'uncertain', errorCode: code(error)});}
    }
  }
  async retry(): Promise<void> {
    const ticket = this.ticket;
    if (!ticket || !this.live(ticket)) return;
    if (!ticket.body && this.state.phase === 'error') {
      ticket.io.abort(); ticket.io = new AbortController();
      this.publish({phase: 'preparing', errorCode: null}); return this.load(ticket);
    }
    if (this.state.phase !== 'uncertain' || !ticket.body) return;
    return this.run(ticket);
  }
  async cancel(): Promise<void> {
    const ticket = this.ticket;
    if (!ticket || !this.live(ticket)) return;
    if (!ticket.body) {this.retire('idle'); return;}
    if (ticket.job && isSelectionActionTerminal(ticket.job.state)) return;
    if (ticket.cancelRequested && this.state.phase === 'cancelling' && ticket.promise) return ticket.promise;
    ticket.cancelRequested = true;
    ticket.io.abort(); ticket.io = new AbortController();
    // Supersede the old local poll/submission, preserving its exact body for ambiguous acceptance.
    ticket.promise = undefined;
    return this.run(ticket);
  }
  dispose() {if (this.disposed) return; this.retire('retired'); this.disposed = true; this.listeners.clear();}
}
