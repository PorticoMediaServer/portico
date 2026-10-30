import {deletionAllowed, parseDeletePreview, parseDeleteResult, type DeletePreview, type DeleteResult} from '../../../../../packages/client-core/src/administration';
import {fetchBulkMetadataPreview, metadataPreviewTargets, type BulkMetadataPreview, type BulkMetadataPreviewTarget} from '../../../../../packages/client-core/src/home-selection/index';
import type {HomeApi} from '../../../../../packages/client-core/src/home';
import {validBulkEdit, validateBulkReceipt, type BulkEdit, type BulkReceipt} from '../../../../../packages/client-core/src/metadata-bulk';
import type {BulkChanges, BulkFieldValue, BulkResultView} from '../../kit/selection';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {CatalogueSelectionOptions} from '../../../../../packages/client-core/src/catalogue-selection';
import type {ContentEntry, LibraryContentApi} from '../../../../../packages/client-core/src/library-content';

const unavailable = (code = 'selection-admin-unavailable') => Object.assign(new Error(code), {code});
const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
/** Bounded, localized copy only: a raw error's message or string is never drawn. */
const failure = (kind: 'edit' | 'delete') => defaultI18n.t('error.title.action', {subject: kind === 'edit' ? 'playlist' : 'other'});

/** Bulk metadata edit and media deletion for one explicit captured leaf selection.
 *
 * The preview is the server's own (schema, shared/mixed values, per-target expected
 * revisions, and the delete preview's files/disk/blocks). Only explicit changes are
 * sent; one operation runs at a time with its plan fixed before the first id is minted;
 * each chunk keeps its operation id and receipt through a retry, and the delete body
 * keeps its revision, confirmation and operation id across an uncertain answer. Every
 * opening has its own epoch: a superseded preview or write can neither publish nor act
 * after a close or a newer opening. The metadata preview's own authority fence is
 * remembered per opening (never compared with a Search or Saved fence), and its exact
 * targets are checked against the captured selection. */
export type NativeBulkMediaSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'saving' | 'result' | 'uncertain' | 'error';
  preview?: BulkMetadataPreview;
  error?: string;
  lockEdited: boolean;
  changes: BulkChanges;
  receipts: readonly BulkReceipt[];
  result?: BulkResultView;
  singleItemFields: BulkMetadataPreview['singleItemFields'];
  values: Readonly<Record<string, BulkFieldValue>>;
  deletion?: Readonly<{phase: 'loading' | 'ready' | 'saving' | 'uncertain' | 'result' | 'error'; preview?: DeletePreview; error?: string;
    confirmation: string; removeFiles: boolean; result?: DeleteResult}>;
}>;

const fieldValue = (preview: BulkMetadataPreview): Readonly<Record<string, BulkFieldValue>> => Object.fromEntries(preview.schema.map(spec => {
  const field = preview.fields[spec.field];
  if (!field) throw unavailable('invalid-metadata-preview');
  return [spec.field, field];
}));
const envelope = (raw: unknown, serverId: string): unknown => {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw unavailable('invalid-admin-envelope');
  const value = raw as {protocolVersion?: unknown; serverId?: unknown; result?: unknown};
  if (typeof value.protocolVersion !== 'string' || !/^\d+\.\d+$/.test(value.protocolVersion) || value.protocolVersion.length > 8) throw unavailable('invalid-admin-envelope');
  if (value.serverId !== serverId) throw unavailable('admin-authority-changed');
  return value.result;
};

export class NativeBulkMedia {
  private readonly options: CatalogueSelectionOptions;
  private readonly entries: readonly ContentEntry[];
  private readonly captured: readonly Readonly<{kind: ContentEntry['kind']; id: string; libraryId: string}>[];
  private readonly targets: readonly BulkMetadataPreviewTarget[];
  private readonly listeners = new Set<() => void>();
  private disposed = false;
  private epoch = 0;
  private controller?: AbortController;
  private flight = false;
  private state: NativeBulkMediaSnapshot = Object.freeze({phase: 'idle', lockEdited: true, changes: {}, receipts: [], singleItemFields: [], values: {}});
  private edits: {body: BulkEdit; receipt?: BulkReceipt}[] = [];
  private baseEdit?: Omit<BulkEdit, 'operationId' | 'targets'>;
  private deleteBody?: Readonly<Record<string, unknown>>;
  private deletePreviewIds?: readonly string[];
  /** The metadata domain's own fence, learned from this opening's first preview. */
  private previewFence?: string;

  constructor(options: CatalogueSelectionOptions, entries: readonly ContentEntry[]) {
    this.options = options;
    this.entries = Object.freeze([...entries]);
    // A Reading selection is one kind of publication (the server edits one kind at a time).
    const reading = entries.some(entry => entry.kind === 'ebook' || entry.kind === 'comic');
    if (!entries.length || entries.length > 200 || new Set(entries.map(entry => entry.id)).size !== entries.length
      || entries.some(entry => !entry.libraryId || !(reading ? entry.kind === entries[0]!.kind && ['ebook', 'comic'].includes(entry.kind)
        : ['movie', 'episode', 'song', 'audiobook_file'].includes(entry.kind)))) throw unavailable();
    this.captured = Object.freeze(entries.map(entry => Object.freeze({kind: entry.kind, id: entry.id, libraryId: entry.libraryId!})));
    this.targets = metadataPreviewTargets(entries.map(entry => ({kind: entry.kind, id: entry.id, libraryId: entry.libraryId!})));
  }

  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.state;
  private publish(patch: Partial<NativeBulkMediaSnapshot>): void {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private live(epoch: number): boolean {
    return !this.disposed && this.epoch === epoch && !this.options.signal.aborted && this.options.isCurrent();
  }
  private assertLive(epoch: number): void {
    if (!this.live(epoch)) {
      if (!this.disposed && (this.options.signal.aborted || !this.options.isCurrent())) this.dispose();
      throw cancelled();
    }
  }
  private publishFor(epoch: number, patch: Partial<NativeBulkMediaSnapshot>): void {if (this.live(epoch)) this.publish(patch);}
  /** A new opening retires every earlier read/write and its retained plan. */
  private begin(patch: Partial<NativeBulkMediaSnapshot>): number {
    const epoch = ++this.epoch;
    this.controller?.abort();
    this.controller = new AbortController();
    this.flight = false; this.edits = []; this.baseEdit = undefined; this.deleteBody = undefined; this.deletePreviewIds = undefined; this.previewFence = undefined;
    this.publish(patch);
    return epoch;
  }
  /** Claimed synchronously, before any await that mints an id or sends a body. */
  private claim(epoch: number, patch: Partial<NativeBulkMediaSnapshot>): boolean {
    if (this.flight || !this.live(epoch)) return false;
    this.flight = true; this.publish(patch); return true;
  }
  private finish(epoch: number): void {if (this.live(epoch)) this.flight = false;}
  private signal(epoch: number): AbortSignal {this.assertLive(epoch); return this.controller!.signal;}
  private async request<T>(epoch: number, path: string, method: string, body: unknown): Promise<T> {
    this.assertLive(epoch);
    const value = await this.options.api.request<T>(path, method, body, this.signal(epoch));
    this.assertLive(epoch);
    return value;
  }
  private previewApi(epoch: number): HomeApi {
    this.assertLive(epoch);
    const api = this.options.api as typeof this.options.api & Pick<LibraryContentApi, 'requestRaw'>;
    // The preparation owner checks currentness and observes late pending handles.
    // Cleanup must still reach this captured API after close/retirement.
    return {request: api.request.bind(api), ...(api.requestRaw ? {requestRaw: api.requestRaw.bind(api)} : {})};
  }
  /** The preview must describe exactly the captured leaves (never a parent expansion). */
  private assertTargets(preview: BulkMetadataPreview, targets: readonly BulkMetadataPreviewTarget[]): void {
    if (preview.serverId !== this.options.scope.serverId || preview.targets.length !== targets.length
      || preview.targets.some((target, index) => target.id !== targets[index]!.id || target.libraryId !== targets[index]!.libraryId || target.kind !== targets[index]!.kind)) {
      throw unavailable('metadata-targets-changed');
    }
    if (this.previewFence !== undefined && preview.viewerFence !== this.previewFence) throw unavailable('metadata-authority-changed');
    this.previewFence = preview.viewerFence;
  }
  private async readPreview(epoch: number, targets: readonly BulkMetadataPreviewTarget[]): Promise<BulkMetadataPreview> {
    const signal = this.signal(epoch), outer = this.options.signal, controller = new AbortController();
    const cancel = () => controller.abort();
    signal.addEventListener('abort', cancel, {once: true}); outer.addEventListener('abort', cancel, {once: true});
    if (signal.aborted || outer.aborted) cancel();
    try {
      return await fetchBulkMetadataPreview(this.previewApi(epoch), {targets}, this.options.scope.serverId, controller.signal, {isCurrent: () => this.live(epoch)});
    } finally {signal.removeEventListener('abort', cancel); outer.removeEventListener('abort', cancel);}
  }

  openEditor = async (): Promise<void> => {
    const epoch = this.begin({phase: 'loading', error: undefined, receipts: [], result: undefined, preview: undefined, changes: {}, values: {}, deletion: undefined});
    if (!this.claim(epoch, {phase: 'loading'})) return;
    try {
      const preview = await this.readPreview(epoch, this.targets);
      this.assertTargets(preview, this.targets);
      this.publishFor(epoch, {phase: 'ready', preview, values: fieldValue(preview), singleItemFields: preview.singleItemFields, lockEdited: true, changes: {}, receipts: [], result: undefined});
    } catch {this.publishFor(epoch, {phase: 'error', error: failure('edit')});}
    finally {this.finish(epoch);}
  };
  setChanges = (changes: BulkChanges): void => {
    // A retained plan (or a write in flight) owns the draft: the same values are retried.
    if (this.edits.length || this.baseEdit || this.flight) return;
    this.publish({changes});
  };
  setLockEdited = (lockEdited: boolean): void => {
    if (this.edits.length || this.baseEdit || this.flight) return;
    this.publish({lockEdited});
  };
  private async plan(epoch: number, preview: BulkMetadataPreview, changes: BulkChanges, lockEdited: boolean): Promise<{base: Omit<BulkEdit, 'operationId' | 'targets'>; edits: {body: BulkEdit}[]}> {
    const fields: Record<string, {value?: string; values?: readonly string[]}> = {};
    const lists: Record<string, {add?: readonly string[]; remove?: readonly string[]}> = {};
    let genres: {add?: readonly string[]; remove?: readonly string[]} | undefined;
    for (const spec of preview.schema) {
      const change = changes[spec.field];
      if (!change) continue;
      if ('value' in change) {fields[spec.field] = {value: change.value}; continue;}
      // The other variant is the list edit; narrowing on the value variant keeps add/remove typed.
      const list = {add: change.add, remove: change.remove};
      if (spec.field === 'genres') genres = list; else lists[spec.field] = list;
    }
    if (!Object.keys(fields).length && !Object.keys(lists).length && !genres) return {base: {}, edits: []};
    const base: Omit<BulkEdit, 'operationId' | 'targets'> = {...(Object.keys(fields).length ? {fields} : {}), ...(Object.keys(lists).length ? {lists} : {}), ...(genres ? {genres} : {}), lockEdited};
    const edits: {body: BulkEdit}[] = [];
    for (let start = 0; start < preview.targets.length; start += 200) {edits.push({body: {...base, operationId: await this.nextId(epoch), targets: preview.targets.slice(start, start + 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision}))}});}
    for (const edit of edits) if (!validBulkEdit(edit.body, preview.schema)) throw unavailable('invalid-bulk-edit');
    return {base, edits};
  }
  private async nextId(epoch: number): Promise<string> {
    this.assertLive(epoch);
    const id = await this.options.requestId();
    this.assertLive(epoch);
    return id;
  }
  private async write(epoch: number, edit: BulkEdit): Promise<BulkReceipt> {
    return validateBulkReceipt(await this.request<unknown>(epoch, routePath('post_metadata_bulk'), 'POST', edit), edit);
  }
  save = async (): Promise<void> => {
    const epoch = this.epoch, preview = this.state.preview, changes = this.state.changes, lockEdited = this.state.lockEdited;
    if (!preview || (this.state.phase !== 'ready' && this.state.phase !== 'uncertain')) return;
    if (!this.claim(epoch, {phase: 'saving', error: undefined})) return;
    try {
      if (!this.edits.length) {
        const planned = await this.plan(epoch, preview, changes, lockEdited);
        this.assertLive(epoch);
        this.baseEdit = planned.base;
        this.edits = planned.edits;
        if (!this.edits.length) {this.publishFor(epoch, {phase: 'result', result: {results: [], updated: 0, failed: 0}}); return;}
      }
      const receipts: BulkReceipt[] = [...this.state.receipts];
      for (const edit of this.edits) {
        if (edit.receipt) continue;
        edit.receipt = await this.write(epoch, edit.body);
        receipts.push(edit.receipt);
        this.publishFor(epoch, {receipts});
      }
      this.publishFor(epoch, {phase: 'result', result: bulkView(receipts)});
    } catch {this.publishFor(epoch, {phase: 'uncertain', error: failure('edit')});}
    finally {this.finish(epoch);}
  };
  /** A retry re-reads the failed leaves' own current revisions, then re-sends only them
   * with the same retained change set; it never reuses a stored expected revision. */
  retry = async (failedIds: readonly string[]): Promise<void> => {
    const epoch = this.epoch, preview = this.state.preview, base = this.baseEdit;
    if (!preview || !base || this.flight || !failedIds.length) return;
    const latest = new Map<string, boolean>();
    for (const receipt of this.state.receipts) for (const row of receipt.results) latest.set(row.id, row.ok);
    const failed = new Set([...latest].filter(([, ok]) => !ok).map(([id]) => id));
    if (failedIds.some(id => !failed.has(id)) || new Set(failedIds).size !== failedIds.length) return;
    const targets = this.targets.filter(target => failedIds.includes(target.id));
    if (!targets.length) return;
    if (!this.claim(epoch, {phase: 'saving', error: undefined})) return;
    try {
      const fresh = await this.readPreview(epoch, targets);
      this.assertTargets(fresh, targets);
      const edits: {body: BulkEdit}[] = [];
      for (let start = 0; start < fresh.targets.length; start += 200) {
        edits.push({body: {...base, operationId: await this.nextId(epoch), targets: fresh.targets.slice(start, start + 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision}))}});
      }
      for (const edit of edits) if (!validBulkEdit(edit.body, fresh.schema)) throw unavailable('invalid-bulk-edit');
      this.edits = edits;
      const receipts: BulkReceipt[] = [...this.state.receipts];
      for (const edit of this.edits) {
        edit.receipt = await this.write(epoch, edit.body);
        receipts.push(edit.receipt);
        this.publishFor(epoch, {receipts});
      }
      this.publishFor(epoch, {phase: 'result', result: bulkView(receipts)});
    } catch {this.publishFor(epoch, {phase: 'uncertain', error: failure('edit')});}
    finally {this.finish(epoch);}
  };

  openDelete = async (): Promise<void> => {
    // Reading selections offer Edit details only; deleting publications is not drawn there.
    if (this.captured.some(entry => entry.kind === 'ebook' || entry.kind === 'comic')) throw unavailable();
    const epoch = this.begin({phase: 'idle', deletion: {phase: 'loading', confirmation: '', removeFiles: false}});
    if (!this.claim(epoch, {deletion: {phase: 'loading', confirmation: '', removeFiles: false}})) return;
    try {
      const result = envelope(await this.request<unknown>(epoch, routePath('post_admin_media_delete_preview'), 'POST', {itemIds: this.captured.map(entry => entry.id)}), this.options.scope.serverId);
      const preview = parseDeletePreview(result);
      this.assertDeletePreview(preview);
      this.deletePreviewIds = preview.targets.map(target => target.itemId);
      this.publishFor(epoch, {deletion: {phase: 'ready', preview, confirmation: '', removeFiles: false}});
    } catch {this.publishFor(epoch, {deletion: {phase: 'error', confirmation: '', removeFiles: false, error: failure('delete')}});}
    finally {this.finish(epoch);}
  };
  /** The preview must cover exactly the captured selection, once each. */
  private assertDeletePreview(preview: DeletePreview): void {
    const ids = preview.targets.map(target => target.itemId);
    if (ids.length !== this.captured.length || new Set(ids).size !== ids.length
      || ids.some(id => !this.captured.some(entry => entry.id === id))
      || preview.targets.some(target => !this.captured.some(entry => entry.id === target.itemId && entry.libraryId === target.libraryId))) {
      throw unavailable('delete-targets-changed');
    }
  }
  setDeletion = (patch: Partial<NonNullable<NativeBulkMediaSnapshot['deletion']>>): void => {
    const deletion = this.state.deletion;
    if (!deletion || this.flight) return;
    this.publish({deletion: {...deletion, ...patch}});
  };
  deleteNow = async (): Promise<void> => {
    const epoch = this.epoch, deletion = this.state.deletion, preview = deletion?.preview;
    if (!deletion || !preview || (deletion.phase !== 'ready' && deletion.phase !== 'uncertain')) return;
    if (!deletionAllowed(preview) || deletion.confirmation.trim() !== preview.confirmation) return;
    if (!this.claim(epoch, {deletion: {...deletion, phase: 'saving', error: undefined}})) return;
    try {
      this.assertDeletePreview(preview);
      if (!this.deleteBody) {
        const operationId = await this.nextId(epoch);
        this.deleteBody = Object.freeze({itemIds: preview.targets.map(target => target.itemId), expectedRevision: preview.revision,
          confirmation: deletion.confirmation, deleteFiles: deletion.removeFiles, operationId});
      }
      const result = parseDeleteResult(envelope(await this.request<unknown>(epoch, routePath('post_admin_media_delete'), 'POST', this.deleteBody), this.options.scope.serverId));
      // One receipt per previewed target, and none for a title that was not previewed.
      const ids = result.receipts.map(receipt => receipt.itemId);
      if (ids.length !== preview.targets.length || new Set(ids).size !== ids.length || ids.some(id => !this.deletePreviewIds?.includes(id))) throw unavailable('delete-result-mismatch');
      this.publishFor(epoch, {deletion: {...deletion, phase: 'result', result, error: undefined}});
    } catch {this.publishFor(epoch, {deletion: {...deletion, phase: 'uncertain', error: failure('delete')}});}
    finally {this.finish(epoch);}
  };
  close = (): void => {
    this.epoch++;
    this.controller?.abort();
    this.controller = undefined;
    this.flight = false; this.edits = []; this.baseEdit = undefined; this.deleteBody = undefined; this.deletePreviewIds = undefined; this.previewFence = undefined;
    this.publish({phase: 'idle', deletion: undefined, error: undefined, receipts: [], changes: {}, preview: undefined, result: undefined, values: {}, singleItemFields: []});
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.epoch++;
    this.controller?.abort();
    this.controller = undefined;
    this.listeners.clear();
  };
}

function bulkView(receipts: readonly BulkReceipt[]): BulkResultView {
  const results = receipts.flatMap(receipt => receipt.results);
  return {results, updated: results.filter(row => row.ok).length, failed: results.filter(row => !row.ok).length};
}

/** The pending changes as one line per field, for the editor's footer summary. */
export function bulkChangeSummary(state: NativeBulkMediaSnapshot): readonly {id: string; text: string}[] {
  return Object.entries(state.changes).flatMap(([field, change]) => {
    const spec = state.preview?.schema.find(candidate => candidate.field === field);
    if (!spec) return [];
    if ('value' in change) return [{id: field, text: `${spec.label}: ${change.value || defaultI18n.t('next.selection.native.notSet')}`}];
    const parts: string[] = [];
    if (change.add.length) parts.push(`+${defaultI18n.list(change.add)}`);
    if (change.remove.length) parts.push(`−${defaultI18n.list(change.remove)}`);
    return parts.length ? [{id: field, text: `${spec.label}: ${parts.join(' · ')}`}] : [];
  });
}
