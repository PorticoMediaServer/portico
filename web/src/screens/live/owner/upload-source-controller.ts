import type {LibraryContentApi} from '@core/library-content.ts';
import {LiveSourceDraft, type LiveSourceInput} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from './lifetime';
import {uploadPreview, sourceReceipt} from './checked';
export type UploadPreview = Readonly<{channels: number; programmes: number; names?: readonly string[]}>;
export type UploadSnapshot = Readonly<{draft: LiveSourceInput; reading: boolean; busy: boolean; preview?: UploadPreview; error?: unknown}>;
const empty: UploadSnapshot = Object.freeze({draft: Object.freeze({id: '', requestId: '', expectedRevision: 0, name: '', playlist: '', guide: '', tunerCount: 0}), reading: false, busy: false});
export class UploadSourceController extends OwnerStore<UploadSnapshot> {
  private draft = new LiveSourceDraft();
  private saving = false;
  private off: () => void;
  constructor(private api: LibraryContentApi, private serverId: string, isCurrent: () => boolean, reportError: (error: unknown) => void) {
    super(empty, isCurrent, reportError);this.snapshot = {...empty, draft: this.draft.getSnapshot()};
    this.off = this.draft.subscribe(() => {this.publish({...this.snapshot, draft: this.draft.getSnapshot(), reading: this.draft.isReading, preview: undefined});});
  }
  edit(patch: Pick<Partial<LiveSourceInput>, 'name' | 'tunerCount'>) {
    if (!this.isActive() || this.saving) return;
    this.invalidate();this.draft.update(patch);this.publish({...this.snapshot, busy: false, error: undefined});
  }
  async read(field: 'playlist' | 'guide', read: () => Promise<string>) {
    if (!this.isActive() || this.saving) return;
    const attempt = this.begin();this.publish({...this.snapshot, busy: false, preview: undefined, error: undefined});
    if (!this.valid(attempt)) return;
    try {
      // Draft.file itself publishes reading=true synchronously. Recheck inside
      // its reader too, before File.text or any caller-supplied I/O begins.
      await this.draft.file(field, () => {if (!this.valid(attempt)) throw {code: 'cancelled'};return read();});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});}
  }
  async check() {
    if (!this.isActive() || this.snapshot.busy || this.draft.isReading || !this.snapshot.draft.name.trim() || !this.snapshot.draft.playlist) return;
    const attempt = this.begin(), draft = this.snapshot.draft;
    this.publish({...this.snapshot, busy: true, error: undefined, preview: undefined});
    if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request('/v1/admin/live-sources/preview', 'POST', draft, attempt.abort.signal);
      if (this.valid(attempt)) this.publish({...this.snapshot, busy: false, preview: uploadPreview(raw, this.serverId)});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});}
  }
  async save() {
    if (!this.isActive() || this.snapshot.busy || this.draft.isReading || !this.snapshot.preview) return false;
    const attempt = this.begin(), draft = this.snapshot.draft;this.saving = true;
    this.publish({...this.snapshot, busy: true, error: undefined});
    try {
      if (!this.valid(attempt)) return false;
      const raw = await this.api.request('/v1/admin/live-sources', 'POST', draft, attempt.abort.signal);
      if (!this.valid(attempt)) return false;
      sourceReceipt(raw, this.serverId, draft.id);
      this.publish({...this.snapshot, busy: false, preview: undefined});return this.valid(attempt);
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});return false;}
    finally {this.saving = false;}
  }
  override dispose() {super.dispose();this.off?.();this.draft?.dispose();}
}
