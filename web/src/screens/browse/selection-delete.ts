import {deletionAllowed, parseDeletePreview, parseDeleteResult, type DeletePreview, type DeleteResult} from '@core/administration.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import type {EntryActionPorts, EntryActionTarget} from '../../shell/entry-actions';

export type LibraryDeletePhase = 'idle' | 'loading' | 'ready' | 'confirming' | 'blocked' | 'deleting' | 'uncertain' | 'result' | 'error';
export type LibraryDeleteSnapshot = Readonly<{
  phase: LibraryDeletePhase; preview?: DeletePreview; confirmation: string; removeFiles: boolean;
  result?: DeleteResult; errorCode?: string;
}>;
type DeleteBody = Readonly<{itemIds: readonly string[]; expectedRevision: number; confirmation: string; deleteFiles: boolean; operationId: string}>;
const idle: LibraryDeleteSnapshot = Object.freeze({phase: 'idle', confirmation: '', removeFiles: true});

function envelope(raw: unknown, serverId: string): unknown {
  const value = raw as {protocolVersion?: unknown; serverId?: unknown; result?: unknown};
  if (value?.protocolVersion !== '1.0' || value.serverId !== serverId) throw new Error('delete_answer_mismatch');
  return value.result;
}

/** One checked bounded explicit deletion: the preview, the exact typed confirmation, and one immutable
 * operationId/body retained for an uncertain retry. Every async answer is fenced to its own open
 * attempt, so a retired or replaced dialog can never delete, apply a receipt or clear a successor. */
export class LibraryDeleteProducer {
  private state: LibraryDeleteSnapshot = idle;
  private listeners = new Set<() => void>();
  private target?: readonly Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'>[]; private ports?: EntryActionPorts;
  private attempt = 0; private controller?: AbortController; private preview?: DeletePreview; private body?: DeleteBody;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => {this.listeners.delete(listener);}; };
  getSnapshot = (): LibraryDeleteSnapshot => this.state;
  private emit() { for (const listener of [...this.listeners]) listener(); }
  private live(attempt: number) { return this.target !== undefined && attempt === this.attempt && this.ports?.isCurrent() === true; }
  private fail(code: string) { this.state = {...this.state, errorCode: code}; this.emit(); }

  open(target: readonly Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'>[], ports: EntryActionPorts) {
    if (!ports.isCurrent() || !target.length || target.length > 200 || new Set(target.map(item => item.itemId)).size !== target.length || new Set(target.map(item => item.libraryId)).size !== 1) { this.close(); return; }
    this.attempt++; this.controller?.abort();
    this.target = Object.freeze(target.map(item => Object.freeze({...item}))); this.ports = ports; this.preview = undefined; this.body = undefined;
    this.state = {...idle, phase: 'loading'}; this.emit();
    void this.load();
  }
  close() {
    this.attempt++; this.controller?.abort(); this.controller = undefined;
    this.target = undefined; this.ports = undefined; this.preview = undefined; this.body = undefined;
    this.state = idle; this.emit();
  }
  setConfirmation(confirmation: string) {
    if (this.state.phase !== 'ready') return;
    this.state = {...this.state, confirmation}; this.emit();
  }
  setRemoveFiles(removeFiles: boolean) {
    if (this.state.phase !== 'ready') return;
    this.state = {...this.state, removeFiles}; this.emit();
  }
  private async load() {
    const attempt = this.attempt, ports = this.ports, target = this.target;
    if (!ports || !target || !ports.isCurrent() || !this.live(attempt)) return;
    const controller = new AbortController(); this.controller = controller;
    try {
      const raw = await ports.api.request(routePath('post_admin_media_delete_preview'), 'POST', {itemIds: target.map(item => item.itemId)}, controller.signal);
      if (!this.live(attempt)) return;
      const preview = parseDeletePreview(envelope(raw, ports.serverId));
      if (preview.targets.length !== target.length || new Set(preview.targets.map(item => item.itemId)).size !== target.length
        || target.some(item => !preview.targets.some(entry => entry.itemId === item.itemId && entry.libraryId === item.libraryId && entry.kind === item.kind))) throw new Error('delete_preview_mismatch');
      this.preview = preview;
      this.state = {phase: deletionAllowed(preview) ? 'ready' : 'blocked', preview, confirmation: '', removeFiles: true};
    } catch {
      if (this.live(attempt)) this.state = {phase: 'error', confirmation: '', removeFiles: true, errorCode: 'delete_preview_failed'};
    } finally {
      if (this.live(attempt)) this.emit();
    }
  }
  async confirm(): Promise<void> {
    const attempt = this.attempt, ports = this.ports, target = this.target, preview = this.preview;
    if (!this.live(attempt) || !ports || !target || !preview || this.state.phase !== 'ready') return;
    if (!deletionAllowed(preview) || this.state.confirmation !== preview.confirmation || preview.targets.length !== target.length
      || target.some(item => !preview.targets.some(entry => entry.itemId === item.itemId && entry.libraryId === item.libraryId && entry.kind === item.kind))) { this.fail('delete_not_ready'); return; }
    if (!this.body) {
      const controller = this.controller;
      this.state = {...this.state, phase: 'confirming', errorCode: undefined}; this.emit();
      try {
        const operationId = await ports.requestId();
        if (!this.live(attempt) || controller?.signal.aborted || this.state.confirmation !== preview.confirmation || !deletionAllowed(preview)) return;
        this.body = Object.freeze({itemIds: Object.freeze(target.map(item => item.itemId)), expectedRevision: preview.revision,
          confirmation: this.state.confirmation, deleteFiles: this.state.removeFiles, operationId});
      } catch { if (this.live(attempt)) { this.state = {...this.state, phase: 'ready'}; this.fail('delete_not_ready'); } return; }
    }
    await this.send(attempt);
  }
  async retry(): Promise<void> { if (this.state.phase === 'uncertain' && this.body) await this.send(this.attempt); }
  private async send(attempt: number) {
    const ports = this.ports, target = this.target, body = this.body;
    if (!ports || !target || !body || !this.live(attempt)) return;
    this.state = {...this.state, phase: 'deleting', errorCode: undefined}; this.emit();
    try {
      const raw = await ports.api.request(routePath('post_admin_media_delete'), 'POST', body, this.controller?.signal);
      if (!this.live(attempt)) return;
      const result = parseDeleteResult(envelope(raw, ports.serverId));
      if (result.receipts.length !== target.length || new Set(result.receipts.map(row => row.itemId)).size !== target.length
        || target.some(item => !result.receipts.some(receipt => receipt.itemId === item.itemId))) throw new Error('delete_result_mismatch');
      this.state = {...this.state, phase: 'result', result, errorCode: undefined};
    } catch {
      if (!this.live(attempt)) return;
      this.state = {...this.state, phase: 'uncertain', errorCode: 'delete_uncertain'};
    } finally {
      if (this.live(attempt)) this.emit();
    }
  }
}

