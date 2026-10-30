import {selectionQuerySelector, type BrowseSelectionContext, type SelectionOperation} from '@core/selection-model/index.ts';
import {fetchBulkMetadataPreview, type BulkMetadataPreview} from '@core/home-selection/index.ts';
import {runSelectionJobs, type HomeJobTask} from '@core/home-selection/execution.ts';
import type {HomeSelectionProgress} from '@core/home-selection/types.ts';
import {jobRequestBody} from '@core/bulk-jobs.ts';
import {bulkDraftValid} from '../../kit/selection/presentation';
import type {BulkEditorDraft} from '../../kit/selection';
import {editorCoreDraft, editorValues} from '../../shell/selection-presentation';
import type {SelectionEditorOptions} from './selection-editor';

type Operation = SelectionOperation<BrowseSelectionContext>;
export type QueryEditorResult = Readonly<{updated: number; failed: number; jobId: string}>;
/** One server-held query, never a list of the loaded cards. The accepted job and
 * original request survive uncertain delivery; a new editor cannot revive it. */
export function createQuerySelectionEditor(options: SelectionEditorOptions) {
  const {api, serverId, viewerFence, requestId, events} = options;
  const signal = options.signal ?? new AbortController().signal;
  const check = () => {if (signal.aborted || !options.isCurrent()) throw new DOMException('Selection retired', 'AbortError');};
  const grants = new WeakMap<BulkMetadataPreview, Operation>();
  const attempts = new WeakMap<BulkMetadataPreview, {key: string; tasks: Promise<HomeJobTask[]>; flight?: Promise<QueryEditorResult>}>();
  return {
    async preview(operation: Operation) {
      check(); const target = operation.target;
      if (target.mode !== 'query' || target.summary.types.length !== 1 || target.summary.unavailableActions?.metadataPreview || !target.summary.every.edit) throw new Error('query-editor-unavailable');
      const query = selectionQuerySelector(target); if (!query.ok) throw new Error('query-editor-refined');
      const fence = viewerFence(); if (!fence) throw new Error('metadata-authority-unavailable');
      const preview = await fetchBulkMetadataPreview(api, {selector: query.selector, expected: query.expected}, serverId, signal, {isCurrent: options.isCurrent}); check();
      if (viewerFence() !== fence || preview.viewerFence !== fence || preview.total !== target.total || preview.contentKind !== target.summary.types[0].kind) throw new Error('metadata-preview-mismatch');
      grants.set(preview, operation); return preview;
    },
    async save(operation: Operation, preview: BulkMetadataPreview, draft: BulkEditorDraft, progress: (value: HomeSelectionProgress) => void): Promise<QueryEditorResult> {
      check();
      if (grants.get(preview) !== operation || preview.viewerFence !== viewerFence() || operation.target.mode !== 'query') throw new Error('metadata-preview-not-current');
      const query = selectionQuerySelector(operation.target); if (!query.ok) throw new Error('query-editor-refined');
      if (!bulkDraftValid(preview.schema, editorValues(preview), draft)) throw new Error('invalid-bulk-edit');
      const args = structuredClone(editorCoreDraft(draft)), key = JSON.stringify(args);
      let attempt = attempts.get(preview);
      if (attempt && attempt.key !== key) throw new Error('metadata-reconcile-required');
      if (attempt?.flight) return attempt.flight;
      if (!attempt) {
        const tasks = Promise.resolve().then(async () => {
          const operationId = (await requestId()).replaceAll('-', ''); check();
          const request = jobRequestBody({operationId, command: 'metadata-edit', selector: query.selector, expected: query.expected, args});
          return [{request, failures: [], observed: false}];
        });
        attempt = {key, tasks}; attempts.set(preview, attempt);
      }
      const retained = attempt;
      retained.flight = Promise.resolve().then(async () => {
        try {
          const current = () => !signal.aborted && options.isCurrent() && viewerFence() === preview.viewerFence;
          const tasks = await retained.tasks; check();
          await runSelectionJobs(api, tasks, signal, current, value => {if (current()) progress({...value, done: value.done + tasks.reduce((sum, task) => sum + (task.job?.failed ?? 0), 0)});}, undefined, events, false);
          check(); if (!current()) throw new Error('metadata-authority-changed');
          const job = tasks[0].job;
          if (!job || job.command !== 'metadata-edit' || !job.totalKnown || job.total !== preview.total || job.done + job.failed !== job.total) throw new Error('metadata-job-mismatch');
          return {updated: job.done, failed: job.failed, jobId: job.jobId};
        } finally {retained.flight = undefined;}
      });
      return retained.flight;
    },
  };
}
