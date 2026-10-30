import {clonePortableDto} from '../clone-portable-dto.ts';
import {createJob, getJob, getJobFailures, isJobTerminal, type Job, type JobArgs, type JobCommand, type JobFailure, type JobRequest, type JobSelector} from '../bulk-jobs.ts';
import type {EventsClient} from '../playback-v1/events.ts';
import type {HomeApi} from '../home.ts';
import type {SelectionIdentity, SelectionTarget} from '../selection-model/index.ts';
import {selectionQuerySelector} from '../selection-model/index.ts';
import type {HomeSelectionProgress} from './types.ts';

export function selectionExecutionError(code: string, retryable = false): Error {
  return Object.assign(new Error(code), {code, retryable});
}
export function explicitIdentities(target: SelectionTarget): readonly SelectionIdentity[] {
  if (target.mode === 'query') {
    const checked = selectionQuerySelector(target);
    throw selectionExecutionError(checked.ok ? 'home-explicit-selection-required' : checked.reason);
  }
  return target.items.map(pick => pick.item);
}
/** Preserve a displayed aggregate as a real container; never substitute its first child. */
export function identitySelector(identity: SelectionIdentity): JobSelector {
  if (identity.kind === 'channel' || identity.kind === 'person') throw selectionExecutionError('catalogue-action-required');
  if (identity.kind === 'show' || identity.kind === 'season' || identity.kind === 'album' || identity.kind === 'artist' || identity.kind === 'book' || identity.kind === 'collection') {
    return {container: {kind: identity.kind, id: identity.id}};
  }
  if (!['movie', 'episode', 'song', 'audiobook_file', 'extra'].includes(identity.kind)) throw selectionExecutionError('selector-kind-unavailable');
  return {items: {ids: [identity.id]}};
}
export type HomeJobTask = {request: JobRequest; job?: Job; attempted?: boolean; rejected?: boolean; failures: readonly JobFailure[]; observed: boolean};
/** In pick order: consecutive playable items share a capped selector, aggregates keep their scope. */
export async function selectionJobTasks(target: SelectionTarget, command: JobCommand, args: JobArgs, requestId: () => Promise<string>): Promise<HomeJobTask[]> {
  if (target.mode === 'query') {
    const checked = selectionQuerySelector(target);
    const capability = command === 'playlist-add' ? 'playlist' : command === 'collection-add' ? 'collection' : undefined;
    if (!checked.ok || !capability || (command === 'playlist-add' && target.summary.unavailableActions?.playlist) || !target.summary.every[capability] || !target.total
      || !('query' in checked.selector) || !/^browse_[a-f0-9]{64}$/.test(checked.expected.catalogRevision)
      || target.summary.library.kind !== 'single' || target.summary.library.libraryId !== checked.selector.query.libraryId
      || !target.summary.types.length || target.summary.types.some(type => !['movie', 'episode', 'song', 'audiobook_file'].includes(type.kind))) throw selectionExecutionError('query-destination-unavailable');
    const selector = clonePortableDto(checked.selector), capturedArgs = clonePortableDto(args), expected = {...checked.expected};
    const operationId = (await requestId()).replaceAll('-', '');
    return [{request: {operationId, command, selector, args: capturedArgs, expected}, failures: [], observed: false}];
  }
  const identities = explicitIdentities(target), selectors: JobSelector[] = [];
  for (const identity of identities) {
    const selector = identitySelector(identity), previous = selectors.at(-1);
    if ('items' in selector && previous && 'items' in previous && previous.items.ids.length < 200) {
      selectors[selectors.length - 1] = {items: {ids: [...previous.items.ids, ...selector.items.ids]}};
    } else selectors.push(selector);
  }
  const out: HomeJobTask[] = [];
  for (const selector of selectors) out.push({request: {operationId: (await requestId()).replaceAll('-', ''), command, selector, args}, failures: [], observed: false});
  return out;
}
function pause(signal: AbortSignal, jobId: string, events?: Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'resyncReads'>>): Promise<void> {
  return new Promise((resolve, reject) => {
    let finished = false;
    let unsubscribe: (() => void) | undefined, offResync: (() => void) | undefined;
    const finish = (cancelled = false) => {
      if (finished) return; finished = true; clearTimeout(timer); unsubscribe?.(); offResync?.(); signal.removeEventListener('abort', stop);
      if (cancelled) reject(selectionExecutionError('cancelled')); else resolve();
    };
    const stop = () => finish(true);
    const timer = setTimeout(() => finish(), 1500);
    unsubscribe = events?.on('job.updated', event => {
      const data = event.data as {jobId?: unknown} | undefined;
      if (event.resource?.id === jobId || data?.jobId === jobId) finish();
    });
    if (!finished) offResync = events?.onResync?.(() => finish());
    if (finished) {unsubscribe?.(); offResync?.();}
    if (!finished) {signal.addEventListener('abort', stop, {once: true}); if (signal.aborted) stop();}
  });
}
/** Durable submission bodies are retained unchanged across missing/ambiguous responses. */
export async function runSelectionJobs(api: HomeApi, tasks: HomeJobTask[], signal: AbortSignal, isCurrent: () => boolean, progress: (value: HomeSelectionProgress, jobIds: readonly string[]) => void, beforeSubmit?: (task: HomeJobTask) => Promise<void>, events?: Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'resyncReads'>>, readFailures = true): Promise<void> {
  const current = () => {if (signal.aborted || !isCurrent()) throw selectionExecutionError('cancelled');};
  const report = () => {
    const jobs = tasks.flatMap(task => task.job ? [task.job] : []);
    progress({phase: jobs.length === tasks.length && jobs.every(job => job.totalKnown) ? 'working' : 'preparing', done: jobs.reduce((n, job) => n + job.done, 0), total: jobs.reduce((n, job) => n + job.total, 0), totalKnown: jobs.length === tasks.length && jobs.every(job => job.totalKnown)}, jobs.map(job => job.jobId));
  };
  const read = <T>(request: () => Promise<T>): Promise<T> => events?.resyncReads ? events.resyncReads.run(signal, request) : request();
  let resyncDebt = 0;
  const offResync = events?.onResync?.(() => {resyncDebt++;});
  try {
    for (const task of tasks) {
      current();
      if (!task.job) {
        if (!task.attempted) {await beforeSubmit?.(task); current(); task.attempted = true;}
        try {task.job = await createJob(api, task.request, signal);}
        catch (error) {
          task.rejected = ['revision_mismatch', 'invalid_request', 'unauthorized', 'not_permitted', 'not_found', 'rate_limited', 'idempotency_key_reused', 'operation_expired'].includes(String((error as {code?: unknown})?.code));
          throw error;
        }
        current();
      }
      report();
      const started = Date.now();
      while (!isJobTerminal(task.job.state)) {
        current();
        if (Date.now() - started > 300000) throw selectionExecutionError('job_poll_timeout', true);
        const answering = resyncDebt;
        task.job = await read(() => getJob(api, task.job!.jobId, signal)); current();
        report();
        if (!isJobTerminal(task.job.state) && answering === resyncDebt) await pause(signal, task.job.jobId, events);
      }
      if (!task.observed) {
        const failures: JobFailure[] = [], cursors = new Set<string>();
        let cursor: string | undefined;
        if (readFailures && task.job.failed > 0) for (let page = 0; page < 50; page++) {
          current(); const result = await read(() => getJobFailures(api, task.job!.jobId, cursor, signal)); current();
          failures.push(...result.items);
          if (!result.nextCursor) break;
          if (cursors.has(result.nextCursor) || page === 49) throw selectionExecutionError('job-failures-incomplete');
          cursors.add(result.nextCursor); cursor = result.nextCursor;
        }
        if (readFailures && failures.length !== task.job.failed) throw selectionExecutionError('job-failures-incomplete');
        task.failures = failures; task.observed = true;
      }
      if (task.job.state === 'failed' && (task.job.errorCode || task.job.failed === 0)) throw selectionExecutionError(task.job.errorCode ?? 'job_failed');
    }
  } finally {offResync?.();}
}
