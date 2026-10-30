import type {HomeSelectionProgress} from '@core/home-selection/types.ts';
import type {JobArgs, JobRequest} from '@core/bulk-jobs.ts';
import type {HomeApi} from '@core/home.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import {selectionQuerySelector, type BrowseSelectionContext, type SelectionContext, type SelectionOperation, type SelectionTarget} from '@core/selection-model/index.ts';
import {runSelectionJobs, selectionJobTasks, type HomeJobTask} from '@core/home-selection/execution.ts';

/** The one place a library selection becomes real work. It hands the model's own
 * target to the existing job planner and runner, so explicit picks stay explicit
 * and a whole view stays the server's query - never a locally enumerated set. */

export type SelectionActionsOptions = Readonly<{
  api: HomeApi;
  events: Pick<EventsClient, 'on'>;
  requestId: () => Promise<string>;
  isCurrent: () => boolean;
  /** The route's lifetime; an absent signal is a private, never-aborted one. */
  signal?: AbortSignal;
  reportError: (error: unknown) => void;
  progress?: (operation: AnyOperation, value: HomeSelectionProgress, jobIds: readonly string[]) => void;
}>;

export type SelectionActionResult = Readonly<{updated: number; failed: number; jobs: readonly string[]}>;

export type PersonalSelectionAction = Readonly<{
  watched?: boolean;
  favorite?: boolean;
  watchlist?: boolean;
}>;

const leafKinds: ReadonlySet<string> = new Set(['movie', 'episode', 'song', 'audiobook_file']);
/** Both selection contexts plan jobs the same way; only the selector/items matter. */
type AnyOperation = SelectionOperation<SelectionContext> | SelectionOperation<BrowseSelectionContext>;

/** A container's own favourite/watched state is a direct command in the canonical
 * dispatcher, not a job over its members. Until that executor is bound here, a
 * personal job is offered only for leaf picks and for the server's leaf views. */
export function personalTargetIsLeaf(target: AnyOperation['target']): boolean {
  if (target.mode === 'query') return true;
  return target.items.length > 0 && target.items.every(pick => leafKinds.has(pick.item.kind));
}

/** Container jobs use the displayed parent's canonical descendant-selection
 * grant, independently of its own title-level personal-state toggles. */
export function personalTargetSupportsJob(target: AnyOperation['target']): boolean {
  return target.mode === 'query' || target.items.length > 0 && target.items.every(pick => leafKinds.has(pick.item.kind)
    || ['show', 'season', 'album', 'artist', 'book', 'collection'].includes(pick.item.kind));
}

export class SelectionActionError extends Error {
  constructor(readonly code: string, readonly retryable = false) {
    super(code);
    this.name = 'SelectionActionError';
  }
}

type RetainedAttempt = {
  tasks: Promise<HomeJobTask[]>;
  current: () => boolean;
  status: 'idle' | 'running' | 'uncertain';
  promise?: Promise<SelectionActionResult>;
};

export function createSelectionActions({api, events, requestId, isCurrent, reportError, progress, signal: lifetime}: SelectionActionsOptions) {
  const routeSignal = lifetime ?? new AbortController().signal;
  // One immutable task body per captured target/action. A lost response or a
  // failed job poll retains the same operation id, selector and args (and any
  // accepted job id), so a retry resumes that work instead of duplicating it.
  const retained = new WeakMap<SelectionTarget, Map<string, RetainedAttempt>>();
  const outcome = (tasks: readonly HomeJobTask[]): SelectionActionResult => Object.freeze({
    updated: tasks.reduce((sum, task) => sum + (task.job?.done ?? 0), 0),
    failed: tasks.reduce((sum, task) => sum + (task.job?.failed ?? 0), 0),
    jobs: Object.freeze(tasks.flatMap(task => task.job ? [task.job.jobId] : []))});
  const run = async (operation: AnyOperation, args: JobArgs, signal: AbortSignal, guard?: () => boolean): Promise<SelectionActionResult> => {
    const target = operation.target as SelectionTarget;
    const current = () => !routeSignal.aborted && isCurrent() && (guard?.() ?? true);
    if (!current() || signal.aborted) throw new SelectionActionError('cancelled');
    if (!personalTargetSupportsJob(target)) throw new SelectionActionError('selection-personal-state-unsupported');
    const grant = args.watched !== undefined ? 'watched' : args.favorite !== undefined ? 'favorite' : args.watchlist !== undefined ? 'myList' : undefined;
    const offered = grant !== undefined && (target.mode === 'query'
      ? target.summary.every[grant] === true
      : target.items.length > 0 && target.items.every(pick => pick.item.capabilities[grant] === true));
    if (!grant || !offered) throw new SelectionActionError('selection-grant-unavailable');
    // A whole view dispatches only its unrefined, checked selector with the exact
    // catalog revision. Exclusions or a kind refinement cannot be dropped silently,
    // so they are refused before any operation id, request or retained body.
    const query = target.mode === 'query' ? selectionQuerySelector(target) : undefined;
    if (query && !query.ok) throw new SelectionActionError(query.reason);
    const key = JSON.stringify(args);
    let attempts = retained.get(target);
    if (!attempts) {attempts = new Map(); retained.set(target, attempts);}
    let attempt = attempts.get(key);
    if (attempt && !attempt.current()) throw new SelectionActionError('cancelled');
    if (attempt?.status === 'running') return attempt.promise!;
    if (!attempt) {
      const tasks = query
        ? requestId().then(id => [{request: Object.freeze({operationId: id.replaceAll('-', ''), command: 'personal-state',
            selector: query.selector, args, expected: query.expected}) as JobRequest, failures: [], observed: false}])
        : selectionJobTasks(target, 'personal-state', args, requestId);
      attempt = {tasks, current: () => current() && !signal.aborted, status: 'idle'};
      attempts.set(key, attempt);
    }
    const captured = attempt;
    captured.status = 'running';
    const promise = (async () => {
      let tasks: HomeJobTask[] = [];
      try {
        tasks = await captured.tasks;
        if (!tasks.length) throw new SelectionActionError('empty-selection');
        await runSelectionJobs(api, tasks, AbortSignal.any([routeSignal, signal]), captured.current, (value, jobs) => {if (captured.current()) progress?.(operation, {...value, done: value.done + tasks.reduce((sum, task) => sum + (task.job?.failed ?? 0), 0)}, jobs);}, undefined, events);
        attempts.delete(key);
        return outcome(tasks);
      } catch (error) {
        const rejected = tasks.some(task => task.rejected || task.job?.state === 'failed' && !!task.job.errorCode);
        if (rejected) attempts.delete(key); else captured.status = 'uncertain';
        throw error;
      } finally {
        captured.promise = undefined;
      }
    })();
    captured.promise = promise;
    return promise;
  };
  const runAction = (operation: AnyOperation, action: PersonalSelectionAction, signal: AbortSignal, guard?: () => boolean) => {
    if (action.watched !== undefined && action.favorite === undefined && action.watchlist === undefined) return run(operation, {watched: action.watched}, signal, guard);
    if (action.favorite !== undefined && action.watched === undefined && action.watchlist === undefined) return run(operation, {favorite: action.favorite}, signal, guard);
    if (action.watchlist !== undefined && action.watched === undefined && action.favorite === undefined) return run(operation, {watchlist: action.watchlist}, signal, guard);
    // One job command carries one personal fact; a mixed request would need a
    // server command that does not exist.
    throw new SelectionActionError('mixed-personal-command');
  };
  return {
    /** The three personal commands the drawn bar offers on a selection. */
    async personal(operation: AnyOperation, action: PersonalSelectionAction, signal: AbortSignal = routeSignal, guard?: () => boolean): Promise<SelectionActionResult> {
      if (action.watched === undefined && action.favorite === undefined && action.watchlist === undefined) throw new SelectionActionError('empty-command');
      return runAction(operation, action, signal, guard);
    },
    reportError,
  };
}

export type SelectionActions = ReturnType<typeof createSelectionActions>;
