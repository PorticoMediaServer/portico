import type {Job as ServerJob, JobItemFailurePage, JobStart} from '../../../contracts/src/next/api.ts';
import {jobViewFilter, type Job} from '../bulk-jobs.ts';
import {serverPath} from '../query/server-api.ts';
import {selectionQuerySelector, type QuerySelectionTarget} from '../selection-model/index.ts';
import type {ContentEntry} from '../library-content.ts';
import type {HomeDestinationReceipt} from './destination-receipt.ts';
import type {HomeSelectionPickerRow, HomeSelectionProgress} from './types.ts';
import {selectionExecutionError, waitForSelectionJob} from './execution.ts';
import {readDestination, type ListDestinationApi} from './list-destinations.ts';
import type {HomeDestinationOptions} from './destinations.ts';

export type ViewDestinationSnapshot = Readonly<{
  row: HomeSelectionPickerRow; receipt: HomeDestinationReceipt; progress: HomeSelectionProgress;
  next: boolean; previous: boolean; cancelling: boolean; jobFailed: boolean;
}>;
const terminal = (job: ServerJob) => !['waiting', 'running'].includes(job.state);
const count = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 0;

/** One server-captured view, with its durable receipt and one bounded failure page. */
export class ViewDestinationJob {
  private job?: ServerJob;
  private flight?: Promise<void>;
  private cancelling = false;
  private failurePage: JobItemFailurePage = {items: []};
  private cursors: (string | undefined)[] = [undefined];
  private pageIndex = 0;
  private readonly o: Readonly<{api: ListDestinationApi; target: QuerySelectionTarget; kind: 'playlist' | 'collection';
    key: string; events: HomeDestinationOptions['events']; signal: AbortSignal; current: () => boolean; changed: (value: ViewDestinationSnapshot) => void}>;
  private row: HomeSelectionPickerRow;
  constructor(options: ViewDestinationJob['o'] & {row: HomeSelectionPickerRow}) {this.o = options; this.row = options.row;}

  private check() {if (this.o.signal.aborted || !this.o.current()) throw selectionExecutionError('cancelled');}
  private snapshot(): ViewDestinationSnapshot {
    const job = this.job!, result = job.result;
    if (!result || !count(result.added) || !count(result.alreadyPresent) || !count(result.failed)) throw selectionExecutionError('list-job-result-unavailable');
    const pending = !terminal(job), failures = this.failurePage.items;
    const failed = result.failed, totalKnown = count(job.progress?.total), done = job.progress?.done ?? 0;
    const observed: Job = {jobId: job.id, command: this.o.kind === 'playlist' ? 'playlist-add' : 'collection-add',
      state: pending ? 'running' : failed || job.state !== 'done' ? 'partial' : 'complete', done,
      total: job.progress?.total ?? 0, totalKnown, failed};
    const receipt: HomeDestinationReceipt = {kind: this.o.kind, destinationId: this.row.id, jobIds: [job.id], jobs: [observed],
      added: result.added, alreadyPresent: result.alreadyPresent,
      failed, unknown: 0, complete: !pending, pending, cancelled: job.state === 'cancelled', unresolvedJobIds: [],
      failures: failures.map(item => ({itemId: item.itemId, code: item.error.code, message: item.error.message})),
      failureEntries: failures.map(item => ({id: item.itemId, title: item.title, kind: this.o.target.summary.types[0]!.kind as ContentEntry['kind']})),
      nextCursor: this.failurePage.nextCursor ?? ''};
    return {row: this.row, receipt, progress: {phase: totalKnown ? 'working' : 'preparing', done, total: job.progress?.total ?? 0, totalKnown},
      next: !!this.failurePage.nextCursor, previous: this.pageIndex > 0, cancelling: this.cancelling,
      jobFailed: job.state === 'failed' && result.failed === 0};
  }
  private publish() {this.check(); this.o.changed(this.snapshot());}
  private async read() {this.check(); this.job = await this.o.api.request<ServerJob>(serverPath('GET /v1/jobs/{id}', {id: this.job!.id}), 'GET', undefined, this.o.signal); this.check();}
  private async failures() {
    if (!this.job?.result?.failed) {this.failurePage = {items: []}; return;}
    this.failurePage = await this.o.api.request<JobItemFailurePage>(serverPath('GET /v1/jobs/{id}/failures', {id: this.job.id,
      query: {limit: 50, cursor: this.cursors[this.pageIndex]}}), 'GET', undefined, this.o.signal);
    this.check();
  }
  run(): Promise<void> {
    if (this.flight) return this.flight;
    this.flight = this.observe().finally(() => {this.flight = undefined;}); return this.flight;
  }
  private async observe() {
    this.check();
    if (!this.job) {
      const checked = selectionQuerySelector(this.o.target);
      if (!checked.ok || !('query' in checked.selector)) throw selectionExecutionError('query-destination-unavailable');
      if (!this.o.api.requestRaw) throw selectionExecutionError('request-headers-unavailable');
      const body: JobStart = {kind: 'add_view_to_list', target: {filter: jobViewFilter(checked.selector.query, true)}, params: {list: this.row.id}};
      const answer = await this.o.api.requestRaw(serverPath('POST /v1/jobs'), 'POST', {headers: {'Idempotency-Key': this.o.key}, body, signal: this.o.signal});
      this.check();
      if (answer.status >= 400) throw selectionExecutionError('list-job-not-started', answer.status >= 500);
      this.job = answer.body as ServerJob;
    } else await this.read();
    while (!terminal(this.job!)) {
      this.publish();
      await waitForSelectionJob(this.o.signal, this.job!.id, this.o.events); await this.read();
    }
    await this.failures();
    this.row = await readDestination(this.o.api, this.o.kind, this.row.id, this.o.signal);
    this.publish();
  }
  async cancel() {
    this.check(); if (!this.job || terminal(this.job) || this.cancelling) return;
    this.cancelling = true; this.publish();
    try {await this.o.api.request(serverPath('DELETE /v1/jobs/{id}', {id: this.job.id}), 'DELETE', undefined, this.o.signal);}
    catch (error) {this.cancelling = false; this.publish(); throw error;}
  }
  async page(previous: boolean) {
    this.check(); if (this.flight || !this.job || !terminal(this.job)) throw selectionExecutionError('picker-busy');
    if (previous) {if (!this.pageIndex) return; this.pageIndex--;}
    else {if (!this.failurePage.nextCursor) return; this.cursors[++this.pageIndex] = this.failurePage.nextCursor;}
    await this.read(); await this.failures(); this.publish();
  }
}
