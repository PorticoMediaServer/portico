import {readCatalogueItemResponse} from '@core/query/catalogue-reads.ts';
import {readMetadataResponse, submitMetadataBulk} from '@core/query/catalogue-actions.ts';
import {catalogueSelectionBulkEdit} from '@core/catalogue-bulk-commands.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {validateRepair} from '@core/metadata-repair.ts';
import {parseMediaItem} from '@core/media-item.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import type {BulkEdit, BulkReceipt} from '@core/metadata-bulk.ts';
import {bulkTargetLimit, validBulkEdit, validateBulkReceipt} from '@core/metadata-bulk.ts';
import {fetchBulkMetadataPreview, metadataPreviewTargets, type BulkMetadataPreview, type BulkMetadataPreviewRequest} from '@core/home-selection/index.ts';
import type {HomeApi} from '@core/home.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {BrowseSelectionContext, SelectionOperation, SelectionTarget} from '@core/selection-model/index.ts';
import type {BulkEditorDraft} from '../../kit/selection';
import {editorCoreDraft} from '../../shell/selection-presentation';

/** The library selection's bulk editor data path. It reuses the same preview
 * parser and job planner the Home editor uses, so a library edit and a Home edit
 * cannot disagree about fields, counts, revisions or receipts. */

export type SelectionEditorOptions = Readonly<{
  api: HomeApi;
  events: Pick<EventsClient, 'on'>;
  requestId: () => Promise<string>;
  isCurrent: () => boolean;
  serverId: string;
  viewerId?: string;
  artwork?: ShellArtwork;
  signal?: AbortSignal;
}>;

type AnyOperation = SelectionOperation<BrowseSelectionContext>;
type BrowseTarget = SelectionTarget<BrowseSelectionContext>;

export class SelectionEditorError extends Error {
  constructor(readonly code: string, readonly retryable = false) {
    super(code);
    this.name = 'SelectionEditorError';
  }
}

/** Only explicit picks are edited: the server's whole-view edit path does not
 * exist yet, so a whole view is refused rather than approximated with loaded ids. */
export function editorPreviewRequest(target: BrowseTarget): BulkMetadataPreviewRequest {
  if (target.mode !== 'explicit' || !target.displayItems.length) throw new SelectionEditorError('whole-view-editor-unavailable');
  return {targets: metadataPreviewTargets(target.displayItems.map(pick => pick.item))};
}

export function createSelectionEditor({api, events, requestId, isCurrent, serverId, viewerId, signal: lifetime}: SelectionEditorOptions) {
  // J-12 (walk, 2 Nov): each answer is checked against the fence its own domain issues, never another surface's. The
  // bulk preview issues the editing fence and every later bulk answer must match it; the metadata reads that name an
  // aggregate's title must match each other. The page's own Home fence exists only while Home is the current document,
  // so a show or library page opened any other way refused every edit before asking the server.
  // The newest preview's fence: a save is granted only for a preview read under it (a preview under a moved fence is the
  // newer answer, step 7), and every chunk and answer of that save is checked against it.
  // Previews are ordered by when they started, as the preferences reader's reads are: an older preview that answers late
  // never takes the editing fence back from a newer one.
  let editingFence: string | undefined, previewsStarted = 0, previewAdopted = 0;
  const routeSignal = lifetime ?? new AbortController().signal;
  const current = (caller?: AbortSignal) => {
    if (!isCurrent() || routeSignal.aborted || caller?.aborted) throw new SelectionEditorError('cancelled');
  };
  const grants = new WeakMap<BulkMetadataPreview, AnyOperation>();
  const attempts = new WeakMap<BulkMetadataPreview, Map<string, {bodies: Promise<readonly BulkEdit[]>; receipts: BulkReceipt[]; flight?: Promise<readonly BulkReceipt[]>}>>();
  return {
    async display(operation: AnyOperation) {
      current(); if (operation.target.mode !== 'explicit') throw new SelectionEditorError('explicit-editor-required');
      const result: {id: string; title: string; posterUrl?: string}[] = [];
      for (const pick of operation.target.displayItems) {
        if (!('libraryId' in pick.item)) throw new SelectionEditorError('metadata-target-unavailable');
        const target = metadataPreviewTargets([pick.item])[0];
        if (target.kind === 'item') {
          const item = parseMediaItem(await readCatalogueItemResponse(api, pick.item.id, routeSignal)); current();
          if (item.id !== pick.item.id || item.libraryId !== pick.item.libraryId || item.kind !== pick.item.kind) throw new SelectionEditorError('metadata-target-mismatch');
          result.push({id: item.id, title: item.title, ...(typeof item.posterUrl === 'string' && item.posterUrl ? {posterUrl: item.posterUrl} : {})});
        } else {
          if (!viewerId) throw new SelectionEditorError('metadata-authority-unavailable');
          const raw = await readMetadataResponse(api, target.kind, target.id, routeSignal); current();
          const data = validateRepair(raw, {serverId, viewerId}, target);
          if (data.snapshot.fields.title === undefined) throw new SelectionEditorError('metadata-target-mismatch');
          const roles = target.kind === 'artist' ? ['portrait', 'square'] : target.kind === 'album' || target.kind === 'book' ? ['cover', 'square', 'poster'] : ['poster'];
          const chosen = roles.flatMap(role => data.snapshot.artwork.filter(choice => choice.role === role && choice.url))[0];
          result.push({id: target.id, title: data.snapshot.fields.title.value, ...(chosen ? {posterUrl: chosen.url} : {})});
        }
      }
      return result;
    },
    async preview(operation: AnyOperation): Promise<BulkMetadataPreview> {
      current();
      if (operation.target.mode !== 'explicit' || !operation.target.items.every(pick => pick.item.capabilities.edit)) throw new SelectionEditorError('metadata-not-offered');
      const request = editorPreviewRequest(operation.target), order = ++previewsStarted;
      const preview = await fetchBulkMetadataPreview(api, request, serverId, routeSignal, {isCurrent}); current();
      // A preview under a moved fence is the newer answer (the viewer's access was decided again): it replaces the editing
      // fence, so Try again after an access change works; a save from an older preview is refused below.
      if (order > previewAdopted) {previewAdopted = order;editingFence = preview.viewerFence;}
      grants.set(preview, operation); return preview;
    },
    /** The same direct bulk endpoint the Home editor uses, so each title gets its
     * own outcome and receipt instead of a job's aggregate counts. */
    async save(operation: AnyOperation, preview: BulkMetadataPreview, draft: BulkEditorDraft, caller?: AbortSignal): Promise<readonly BulkReceipt[]> {
      current(caller);
      if (grants.get(preview) !== operation || preview.viewerFence !== editingFence) throw new SelectionEditorError('metadata-preview-not-current');
      const core = structuredClone(editorCoreDraft(draft)), key = JSON.stringify(core);
      let retained = attempts.get(preview); if (!retained) {retained = new Map(); attempts.set(preview, retained);}
      // An uncertain write must finish with its exact original body before a new draft.
      if ([...retained.keys()].some(value => value !== key)) throw new SelectionEditorError('metadata-reconcile-required');
      let attempt = retained.get(key);
      if (attempt?.flight) return attempt.flight;
      if (!attempt) {
        const targets = preview.targets;
        const bodies = (async () => {
          const result: BulkEdit[] = [];
          for (let start = 0; start < targets.length; start += bulkTargetLimit) {
            const operationId = await requestId();
            const body = catalogueSelectionBulkEdit(core, targets, start, operationId);
            current(caller); if (!validBulkEdit(body, preview.schema)) throw new SelectionEditorError('invalid-bulk-edit');
            result.push(body);
          }
          return result;
        })();
        attempt = {bodies, receipts: []}; retained.set(key, attempt);
      }
      const captured = attempt;
      captured.flight = Promise.resolve().then(async () => {
        try {
          const bodies = await captured.bodies;
          for (let index = captured.receipts.length; index < bodies.length; index++) {
            current(caller); if (editingFence !== preview.viewerFence) throw new SelectionEditorError('metadata-authority-changed');
            const body = bodies[index];
            const raw = await submitMetadataBulk(api, body, caller ? AbortSignal.any([routeSignal, caller]) : routeSignal);
            current(caller); if (editingFence !== preview.viewerFence) throw new SelectionEditorError('metadata-authority-changed');
            captured.receipts.push(validateBulkReceipt(raw, body));
          }
          return Object.freeze([...captured.receipts]);
        } finally {captured.flight = undefined;}
      });
      return captured.flight;
    },
  };
}

export type SelectionEditor = ReturnType<typeof createSelectionEditor>;

/** A whole-view query target's selector is what the preview expects; exported for
 * the adapter's gate so it can refuse a refined view before opening an editor. */
export function editorTargetLawful(target: BrowseTarget | null | undefined): boolean {
  return !!target && target.mode === 'explicit' && target.displayItems.length > 0;
}

