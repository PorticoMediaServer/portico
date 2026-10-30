import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {RefreshStatus} from '@core/linear-api.ts';
import {canRefreshSource} from '@core/server-admin/panel-logic.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts, terminal} from './lifetime';
import {sourceDocument, actionReceipt} from './checked';

export type OwnerLiveSource = Readonly<{
  id: string; name: string; revision: number; state: string; acquisition?: 'upload' | 'remote'; tunerCount: number;
  tunerCountMode: string; generation: string; publishedAt: string; channels: number;
  programmes: number; diagnostics?: unknown;
}>;
export type SourceDocument = Readonly<{sources: readonly OwnerLiveSource[]; refresh: readonly RefreshStatus[]}>;
export type SourceSnapshot = Readonly<{document?: SourceDocument; loading: boolean; busy?: string; error?: unknown; checkedAt?: number}>;
const sourcePath = '/v1/admin/live-sources';

/** Uncertain commands retain their immutable body and id across A/B/A. */
export class LiveSourceController extends OwnerStore<SourceSnapshot> {
  private quietSerial = 0;
  private quietRead?: AbortController;
  private operations = new Map<string, Readonly<Record<string, unknown>>>();
  constructor(private api: LibraryContentApi, private serverId: string, isCurrent: () => boolean, reportError: (error: unknown) => void,
    private requestId: () => string = channelRequestID) {super(Object.freeze({loading: false}), isCurrent, reportError);}
  invalidateQuiet = () => {++this.quietSerial;this.quietRead?.abort();this.quietRead = undefined;};
  refreshQuietly = async (): Promise<QuietRefreshOutcome> => {
    if (!this.isActive() || this.snapshot.loading) return {kind: 'deferred'};
    this.invalidateQuiet();const read = this.quietRead = new AbortController(), serial = this.quietSerial;
    const owns = () => this.isActive() && !read.signal.aborted && this.quietSerial === serial;
    try {
      const raw = await this.api.request(sourcePath, 'GET', undefined, read.signal);
      if (!owns()) return {kind: 'deferred'};
      const document = sourceDocument(raw, this.serverId);
      this.publish({...this.snapshot, document, checkedAt: Date.now()});
      return {kind: 'adopted'};
    } catch (error) {
      if (!owns()) return {kind: 'deferred'};
      if (terminal(error)) this.fail(error, this.snapshot);
      return {kind: 'unreadable', error};
    } finally {if (this.quietRead === read) this.quietRead = undefined;};
  };
  async load() {
    if (!this.isActive()) return;
    this.invalidateQuiet();const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});
    if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request(sourcePath, 'GET', undefined, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      const document = sourceDocument(raw, this.serverId);
      this.publish({...this.snapshot, document, checkedAt: Date.now(), loading: false});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, busy: undefined, error: errorFacts(error)});}
  }
  async act(source: OwnerLiveSource, action: 'refresh' | 'toggle' | 'remove') {
    if (!this.isActive() || this.snapshot.busy) return false;
    const stored = this.snapshot.document?.sources.find(row => row.id === source.id && row.revision === source.revision);
    if (!stored) return false;
    if (action === 'refresh' && (stored.acquisition === 'upload' || !canRefreshSource(stored, this.snapshot.document!.refresh) || this.snapshot.document!.refresh.some(row => row.sourceId === stored.id && row.state === 'refreshing'))) return false;
    const key = JSON.stringify([stored.id, stored.revision, action, stored.state]);
    let body = this.operations.get(key);
    if (!body && this.operations.size >= 256) return false;
    if (!body) {body = Object.freeze({expectedRevision: stored.revision, ...(action === 'refresh' ? {} : {requestId: this.requestId()}), ...(action === 'toggle' ? {enabled: stored.state === 'disabled'} : {})});this.operations.set(key, body);}
    this.invalidateQuiet();const attempt = this.begin();this.publish({...this.snapshot, busy: stored.id, error: undefined});
    if (!this.valid(attempt)) return false;
    try {
      const receipt = await this.api.request(`${sourcePath}/${encodeURIComponent(stored.id)}/${action === 'toggle' ? 'enabled' : action}`, 'POST', body, attempt.abort.signal);
      if (!this.valid(attempt)) return false;
      actionReceipt(receipt, this.serverId, action);
      this.publish({...this.snapshot, busy: undefined});
      if (!this.valid(attempt)) return false;
      await this.load();return this.isActive();
    } catch (error) {
      if (this.valid(attempt)) {
        this.fail(error, {...this.snapshot, loading: false, busy: undefined, error: errorFacts(error)});
        if (this.valid(attempt) && errorFacts(error).status === 409) {await this.load();this.publish({...this.snapshot, error: errorFacts(error)});}
      }
      return false;
    }
  }
  override dispose() {this.invalidateQuiet();this.operations.clear();super.dispose();}
}
