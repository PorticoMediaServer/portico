import type {LibraryContentApi} from '@core/library-content.ts';
import {readRecordingRoot, saveRecordingRoot, type RecordingRootStatus, type RecordingRootIntent} from '@core/server-admin/recording-root.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
type State = Readonly<{loading: boolean; saving: boolean; status?: RecordingRootStatus; draft?: string; error?: unknown; unsupported?: boolean; uncertain?: boolean; terminalRequest?: 'console_expired' | 'console_capacity'}>;
export class RecordingRootController extends OwnerStore<State> {
  private pending?: RecordingRootIntent;
  constructor(private api: LibraryContentApi, current: () => boolean, reportError: (error: unknown) => void) {super(Object.freeze({loading: false, saving: false}), current, reportError);}
  dirty = () => {const s = this.getSnapshot();return !!s.status && s.draft !== undefined && s.draft !== (s.status.requested || s.status.effective);};
  async load(keepDraft = false) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.pending || !keepDraft && this.dirty()) return;
    const priorDraft = this.snapshot.draft, terminalRequest = this.snapshot.terminalRequest;
    const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});
    try {
      const status = await readRecordingRoot(this.api, attempt.abort.signal);
      if (this.valid(attempt)) this.publish({loading: false, saving: false, status, terminalRequest, draft: keepDraft ? priorDraft ?? (status.requested || status.effective) : status.requested || status.effective});
    } catch (error) {
      if (!this.valid(attempt)) return;
      const facts = errorFacts(error);
      if (facts.status === 404 || facts.code === 'not_found') this.publish({loading: false, saving: false, unsupported: true});
      else this.fail(error, {...this.snapshot, loading: false, error: facts});
    }
  }
  choose(path: string) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.pending || !this.snapshot.status || this.snapshot.status.operatorControlled || !path || new TextEncoder().encode(path).length > 1024) return;
    this.publish({...this.snapshot, draft: path, error: undefined, terminalRequest: undefined});
  }
  discard() {
    if (this.isActive() && !this.snapshot.saving && !this.pending && this.snapshot.status) this.publish({...this.snapshot, draft: this.snapshot.status.requested || this.snapshot.status.effective, error: undefined});
  }
  async save() {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || !this.snapshot.status || !this.snapshot.draft || this.snapshot.status.operatorControlled || !this.dirty()) return;
    const intent = this.pending ?? Object.freeze({expectedRevision: this.snapshot.status.revision, operationId: channelRequestID(), path: this.snapshot.draft});
    this.pending = intent;
    const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});
    try {
      const status = await saveRecordingRoot(this.api, intent, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      this.pending = undefined;this.publish({loading: false, saving: false, status, draft: status.requested || status.effective, uncertain: false});
    } catch (error) {
      if (!this.valid(attempt)) return;
      const facts = errorFacts(error);
      if (facts.code === 'console_expired' || facts.code === 'console_capacity') {
        // These typed responses definitively retire this operation key. Expiry
        // does not prove it never succeeded: reread, never remint/replay a write.
        this.pending = undefined;
        this.publish({loading: false, saving: false, terminalRequest: facts.code, error: facts});
        await this.load();
        return;
      }
      const refused = [400, 409, 422].includes(facts.status ?? 0) || ['invalid_request', 'invalid_console_request', 'console_conflict', 'revision_mismatch'].includes(facts.code);
      if (refused) this.pending = undefined;
      this.fail(error, {...this.snapshot, saving: false, uncertain: !refused, error: facts});
    }
  }
  override dispose() {this.pending = undefined;super.dispose();}
}
