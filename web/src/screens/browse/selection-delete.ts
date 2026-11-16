import {readCatalogueDeletion, deleteCatalogueTitles, type DeletionTarget} from '@core/query/catalogue-actions.ts';
import {deletionAllowed, type DeletePreview, type DeleteResult} from '@core/administration.ts';
import type {EntryActionPorts, EntryActionTarget} from '../../shell/entry-actions';

export type LibraryDeletePhase = 'idle' | 'loading' | 'ready' | 'confirming' | 'blocked' | 'deleting' | 'uncertain' | 'result' | 'error';
export type LibraryDeleteSnapshot = Readonly<{
  phase: LibraryDeletePhase; preview?: DeletePreview; confirmation: string; removeFiles: boolean;
  result?: DeleteResult; errorCode?: string;
}>;
const idle: LibraryDeleteSnapshot = Object.freeze({phase: 'idle', confirmation: '', removeFiles: true});

/** One checked bounded explicit deletion: the preview, the exact typed confirmation, and the titles confirmed, kept for
 * an uncertain retry. Every async answer is fenced to its own open
 * attempt, so a retired or replaced dialog can never delete, apply a receipt or clear a successor. */
export class LibraryDeleteProducer {
  private state: LibraryDeleteSnapshot = idle;
  private listeners = new Set<() => void>();
  private target?: readonly DeletionTarget[]; private ports?: EntryActionPorts;
  private attempt = 0; private controller?: AbortController; private preview?: DeletePreview; private body?: readonly DeletionTarget[];
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => {this.listeners.delete(listener);}; };
  getSnapshot = (): LibraryDeleteSnapshot => this.state;
  private emit() { for (const listener of [...this.listeners]) listener(); }
  private live(attempt: number) { return this.target !== undefined && attempt === this.attempt && this.ports?.isCurrent() === true; }
  private fail(code: string) { this.state = {...this.state, errorCode: code}; this.emit(); }

  open(target: readonly (Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'> & Partial<Pick<EntryActionTarget, 'title'>>)[], ports: EntryActionPorts) {
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
      const preview = await readCatalogueDeletion(ports.api, target, controller.signal);
      if (!this.live(attempt)) return;
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
    this.body ??= target;
    await this.send(attempt);
  }
  async retry(): Promise<void> { if (this.state.phase === 'uncertain' && this.body) await this.send(this.attempt); }
  private async send(attempt: number) {
    const ports = this.ports, target = this.target, body = this.body;
    if (!ports || !target || !body || !this.live(attempt)) return;
    this.state = {...this.state, phase: 'deleting', errorCode: undefined}; this.emit();
    try {
      const result = await deleteCatalogueTitles(ports.api, body, this.controller?.signal);
      if (!this.live(attempt)) return;
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

