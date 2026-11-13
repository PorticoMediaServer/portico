import {readOwnerSources, changeOwnerSource, type OwnerSource} from '@core/server-admin/live-sources.ts';
import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {OwnerStore, errorFacts, terminal} from './lifetime';
export type OwnerLiveSource = OwnerSource;
export type SourceDocument = {sources: readonly OwnerLiveSource[]};
export type SourceSnapshot = Readonly<{document?: SourceDocument; loading: boolean; busy?: string; error?: unknown; checkedAt?: number}>;

/** Commands use the displayed source revision; a separate quiet read never cancels them. */
export class LiveSourceController extends OwnerStore<SourceSnapshot> {
  private quietSerial = 0;
  private quietRead?: AbortController;
  constructor(private api: LibraryContentApi, _serverId: string, isCurrent: () => boolean, reportError: (error: unknown) => void, _requestId?: () => string) {super(Object.freeze({loading: false}), isCurrent, reportError);}
  invalidateQuiet = () => {++this.quietSerial;this.quietRead?.abort();this.quietRead = undefined;};
  refreshQuietly = async (): Promise<QuietRefreshOutcome> => {
    if (!this.isActive() || this.snapshot.loading) return {kind: 'deferred'};
    this.invalidateQuiet();const read = this.quietRead = new AbortController(), serial = this.quietSerial;
    const owns = () => this.isActive() && !read.signal.aborted && this.quietSerial === serial;
    try {
      const document = await readOwnerSources(this.api, read.signal);
      if (!owns()) return {kind: 'deferred'};
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
      const document = await readOwnerSources(this.api, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      this.publish({...this.snapshot, document, checkedAt: Date.now(), loading: false});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, busy: undefined, error: errorFacts(error)});}
  }
  async act(source: OwnerLiveSource, action: 'refresh' | 'toggle' | 'remove') {
    if (!this.isActive() || this.snapshot.busy) return false;
    const stored = this.snapshot.document?.sources.find(row => row.id === source.id && row.revision === source.revision);
    if (!stored) return false;
    if (action === 'refresh' && (stored.paused || stored.state === 'refreshing')) return false;
    this.invalidateQuiet();const attempt = this.begin();this.publish({...this.snapshot, busy: stored.id, error: undefined});
    if (!this.valid(attempt)) return false;
    try {
      await changeOwnerSource(this.api, stored, action, attempt.abort.signal);
      if (!this.valid(attempt)) return false;
      this.publish({...this.snapshot, busy: undefined});
      if (!this.valid(attempt)) return false;
      await this.load();return this.isActive();
    } catch (error) {
      if (this.valid(attempt)) {
        this.fail(error, {...this.snapshot, loading: false, busy: undefined, error: errorFacts(error)});
        if (this.valid(attempt) && [409, 412].includes(errorFacts(error).status ?? 0)) {await this.load();this.publish({...this.snapshot, error: errorFacts(error)});}
      }
      return false;
    }
  }
  override dispose() {this.invalidateQuiet();super.dispose();}
}
