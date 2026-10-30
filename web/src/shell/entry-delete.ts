import {deletionAllowed, parseDeletePreview, parseDeleteResult, type DeletePreview, type DeleteResult} from '@core/administration.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import type {EntryActionPorts, EntryActionTarget} from './entry-actions';

export type EntryDeletePhase = 'idle' | 'loading' | 'ready' | 'confirming' | 'blocked' | 'deleting' | 'uncertain' | 'result' | 'error';
export type EntryDeleteSnapshot = Readonly<{
  phase: EntryDeletePhase; preview?: DeletePreview; confirmation: string; removeFiles: boolean;
  result?: DeleteResult; errorCode?: string;
}>;
type DeleteBody = Readonly<{itemIds: readonly string[]; expectedRevision: number; confirmation: string; deleteFiles: boolean; operationId: string}>;
const idle: EntryDeleteSnapshot = Object.freeze({phase: 'idle', confirmation: '', removeFiles: true});

function envelope(raw: unknown, serverId: string): unknown {
  const value = raw as {protocolVersion?: unknown; serverId?: unknown; result?: unknown};
  if (value?.protocolVersion !== '1.0' || value.serverId !== serverId) throw new Error('delete_answer_mismatch');
  return value.result;
}

/** One checked single-title deletion: the preview, the exact typed confirmation, and one immutable
 * operationId/body retained for an uncertain retry. Every async answer is fenced to its own open
 * attempt, so a retired or replaced dialog can never delete, apply a receipt or clear a successor. */
export class EntryDeleteProducer {
  private state: EntryDeleteSnapshot = idle;
  private listeners = new Set<() => void>();
  private target?: EntryActionTarget; private ports?: EntryActionPorts;
  private attempt = 0; private controller?: AbortController; private preview?: DeletePreview; private body?: DeleteBody;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => {this.listeners.delete(listener);}; };
  getSnapshot = (): EntryDeleteSnapshot => this.state;
  private emit() { for (const listener of [...this.listeners]) listener(); }
  private live(attempt: number) { return this.target !== undefined && attempt === this.attempt && this.ports?.isCurrent() === true; }
  private fail(code: string) { this.state = {...this.state, errorCode: code}; this.emit(); }

  open(target: EntryActionTarget, ports: EntryActionPorts) {
    if (!ports.isCurrent()) { this.close(); return; }
    this.attempt++; this.controller?.abort();
    this.target = target; this.ports = ports; this.preview = undefined; this.body = undefined;
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
      const raw = await ports.api.request(routePath('post_admin_media_delete_preview'), 'POST', {itemIds: [target.itemId]}, controller.signal);
      if (!this.live(attempt)) return;
      const preview = parseDeletePreview(envelope(raw, ports.serverId)), entry = preview.targets[0];
      if (preview.targets.length !== 1 || !entry || entry.itemId !== target.itemId || entry.libraryId !== target.libraryId) throw new Error('delete_preview_mismatch');
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
    const entry = preview.targets[0];
    if (!deletionAllowed(preview) || this.state.confirmation !== preview.confirmation || preview.targets.length !== 1
      || !entry || entry.itemId !== target.itemId || entry.libraryId !== target.libraryId) { this.fail('delete_not_ready'); return; }
    if (!this.body) {
      const controller = this.controller;
      this.state = {...this.state, phase: 'confirming', errorCode: undefined}; this.emit();
      try {
        const operationId = await ports.requestId();
        if (!this.live(attempt) || controller?.signal.aborted || this.state.confirmation !== preview.confirmation || !deletionAllowed(preview)) return;
        this.body = Object.freeze({itemIds: Object.freeze([target.itemId]), expectedRevision: preview.revision,
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
      const result = parseDeleteResult(envelope(raw, ports.serverId)), receipt = result.receipts[0];
      if (result.receipts.length !== 1 || !receipt || receipt.itemId !== target.itemId || new Set(result.receipts.map(row => row.itemId)).size !== 1) throw new Error('delete_result_mismatch');
      this.state = {...this.state, phase: 'result', result, errorCode: undefined};
    } catch {
      if (!this.live(attempt)) return;
      this.state = {...this.state, phase: 'uncertain', errorCode: 'delete_uncertain'};
    } finally {
      if (this.live(attempt)) this.emit();
    }
  }
}

/** The host maps an uncertain delete to the same approved retry dialog the selection surface uses. */
export function deleteSurfaceFor(phase: EntryDeletePhase): 'dialog' | 'uncertain' | 'none' {
  if (phase === 'idle') return 'none';
  return phase === 'uncertain' ? 'uncertain' : 'dialog';
}
