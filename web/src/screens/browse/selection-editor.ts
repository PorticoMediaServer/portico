import type {ShellArtwork} from '../../shell/artwork';
import {validateRepair} from '@core/metadata-repair.ts';
import {parseMediaItem} from '@core/media-item.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import type {BulkEdit, BulkReceipt} from '@core/metadata-bulk.ts';
import {validBulkEdit, validateBulkReceipt} from '@core/metadata-bulk.ts';
import {fetchBulkMetadataPreview, metadataPreviewTargets, type BulkMetadataPreview, type BulkMetadataPreviewRequest} from '@core/home-selection/index.ts';
import type {HomeApi} from '@core/home.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {BrowseSelectionContext, SelectionOperation, SelectionTarget} from '@core/selection-model/index.ts';
import type {BulkEditorDraft} from '../../kit/selection';
import {editorCoreDraft} from '../../shell/selection-presentation';
import {routePath} from '../../../../packages/contracts/src/routes.ts';

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
  viewerFence: () => string | undefined;
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

export function createSelectionEditor({api, events, requestId, isCurrent, serverId, viewerId, viewerFence, signal: lifetime}: SelectionEditorOptions) {
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
          const item = parseMediaItem(await api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(pick.item.id)}), 'GET', undefined, routeSignal)); current();
          if (item.id !== pick.item.id || item.libraryId !== pick.item.libraryId || item.kind !== pick.item.kind) throw new SelectionEditorError('metadata-target-mismatch');
          result.push({id: item.id, title: item.title, ...(typeof item.posterUrl === 'string' && item.posterUrl ? {posterUrl: item.posterUrl} : {})});
        } else {
          if (!viewerId) throw new SelectionEditorError('metadata-authority-unavailable');
          const fence = viewerFence();
          const raw = await api.request(encodedRoutePath('get_metadata_kind_id', {kind: encodeURIComponent(target.kind), id: encodeURIComponent(target.id)}), 'GET', undefined, routeSignal); current();
          const data = validateRepair(raw, {serverId, viewerId}, target);
          if (!fence || data.viewerFence !== fence || viewerFence() !== fence || data.snapshot.fields.title === undefined) throw new SelectionEditorError('metadata-target-mismatch');
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
      const fence = viewerFence(); if (!fence) throw new SelectionEditorError('metadata-authority-unavailable');
      const request = editorPreviewRequest(operation.target);
      const preview = await fetchBulkMetadataPreview(api, request, serverId, routeSignal, {isCurrent}); current();
      if (preview.viewerFence !== fence || viewerFence() !== fence) throw new SelectionEditorError('metadata-authority-changed');
      grants.set(preview, operation); return preview;
    },
    /** The same direct bulk endpoint the Home editor uses, so each title gets its
     * own outcome and receipt instead of a job's aggregate counts. */
    async save(operation: AnyOperation, preview: BulkMetadataPreview, draft: BulkEditorDraft, caller?: AbortSignal): Promise<readonly BulkReceipt[]> {
      current(caller);
      if (grants.get(preview) !== operation || preview.viewerFence !== viewerFence()) throw new SelectionEditorError('metadata-preview-not-current');
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
          for (let start = 0; start < targets.length; start += 200) {
            const body: BulkEdit = {operationId: await requestId(), targets: targets.slice(start, start + 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision})), ...core};
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
            current(caller); if (viewerFence() !== preview.viewerFence) throw new SelectionEditorError('metadata-authority-changed');
            const body = bodies[index];
            const raw = await api.request(routePath('post_metadata_bulk'), 'POST', body, caller ? AbortSignal.any([routeSignal, caller]) : routeSignal);
            current(caller); if (viewerFence() !== preview.viewerFence) throw new SelectionEditorError('metadata-authority-changed');
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

