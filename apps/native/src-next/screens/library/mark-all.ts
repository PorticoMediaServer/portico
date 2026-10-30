import {createJob, getJob, isJobTerminal, type BulkJobsApi, type Job, type JobSelector} from '../../../../../packages/client-core/src/bulk-jobs';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';

/** library-mark-all (tv.html frame 4's menu, phone-app 4c): one durable server job over
 * the current view. The confirmation names the view's own count, and every progress and
 * result number is the job's, never a client tally or an estimate. */
export type NativeMarkAllSnapshot = Readonly<{
  open: boolean;
  phase: 'confirm' | 'working' | 'done' | 'failed';
  /** The view's own count, as the browse result reported it before submitting. */
  viewTotal?: number;
  viewAtLeast: boolean;
  /** The job's captured membership total; absent until the server has counted it. */
  total?: number;
  totalKnown: boolean;
  done: number;
  failed: number;
  error?: unknown;
}>;

export type NativeMarkAllRequest = Readonly<{
  selector: JobSelector;
  pivot: string;
  viewTotal?: number;
  viewAtLeast: boolean;
  read: boolean;
  /** The view that submitted it may refresh itself once the job is applied. */
  applied?: () => void;
}>;

type Options = NativeMarkAllRequest & Readonly<{
  api: BulkJobsApi;
  requestId: () => Promise<string>;
  current: () => boolean;
  finished: () => void;
}>;

const POLL_MS = 1000, TIMEOUT_MS = 300000;

function wait(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {clearTimeout(timer); reject(Object.assign(new Error('cancelled'), {name: 'AbortError'}));}, {once: true});
  });
}

export class NativeLibraryMarkAll {
  private value: NativeMarkAllSnapshot;
  private readonly options: Options;
  private readonly listeners = new Set<() => void>();
  private abort?: AbortController;
  private disposed = false;

  constructor(options: Options) {
    this.options = options;
    this.value = Object.freeze({open: true, phase: 'confirm', ...(options.viewTotal === undefined ? {} : {viewTotal: options.viewTotal}),
      viewAtLeast: options.viewAtLeast, totalKnown: false, done: 0, failed: 0});
  }

  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeMarkAllSnapshot => {this.current(); return this.value;};
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private patch(change: Partial<NativeMarkAllSnapshot>): void {
    if (!this.current()) return;
    this.value = Object.freeze({...this.value, ...change});
    for (const listener of [...this.listeners]) listener();
  }
  private publish(job: Job): void {
    this.patch({done: job.done, failed: job.failed, totalKnown: job.totalKnown,
      ...(job.totalKnown ? {total: job.total} : {})});
  }

  /** Submit once. The job captures its own membership, so the progress shown is the
   * server's: `totalKnown` false means the count is still being prepared. */
  start = async (): Promise<void> => {
    if (!this.current() || this.value.phase === 'working' || this.value.phase === 'done') return;
    this.patch({phase: 'working', error: undefined});
    const controller = new AbortController(); this.abort = controller;
    try {
      const operationId = await this.options.requestId();
      if (!this.current()) return;
      let job = await createJob(this.options.api, {operationId, command: 'personal-state',
        selector: this.options.selector, args: {watched: true}}, controller.signal);
      if (!this.current()) return;
      this.publish(job);
      const started = Date.now();
      while (!isJobTerminal(job.state)) {
        if (Date.now() - started > TIMEOUT_MS) {
          throw Object.assign(new Error('The bulk action is still running. Open the library again to check it.'), {code: 'job_poll_timeout'});
        }
        await wait(POLL_MS, controller.signal);
        if (!this.current()) return;
        job = await getJob(this.options.api, job.jobId, controller.signal);
        if (!this.current()) return;
        this.publish(job);
      }
      if (job.state === 'failed') {
        throw Object.assign(new Error('The bulk action could not finish.'), {code: job.errorCode ?? 'job_failed'});
      }
      this.patch({phase: 'done', done: job.done, failed: job.failed});
      this.options.applied?.();
    } catch (error) {
      if (!this.current() || controller.signal.aborted) return;
      this.patch({phase: 'failed', error});
    }
  };

  /** Closing while the job runs stops this view's polling only; the server job is durable. */
  close = (): void => {this.options.finished(); this.dispose();};

  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.abort?.abort(); this.abort = undefined;
    this.value = Object.freeze({...this.value, open: false});
    for (const listener of [...this.listeners]) listener();
    this.listeners.clear();
  };

  /** The body line for the confirmation, from the two catalogued sentences. */
  body = (): 'next.browse.native.markAllReadBody' | 'next.browse.native.markAllWatchedBody' =>
    this.options.read ? 'next.browse.native.markAllReadBody' : 'next.browse.native.markAllWatchedBody';
  pivot = (): string => this.options.pivot;
  read = (): boolean => this.options.read;
  errorBody = (): string | undefined => this.value.error === undefined ? undefined : presentError(this.value.error, 'library', {operation: 'action'}).body;
}
