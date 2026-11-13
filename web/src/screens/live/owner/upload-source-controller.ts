import {previewOwnerSource, createOwnerSource, type SourceSave, type OwnerSourcePreview} from '@core/server-admin/live-sources.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {LiveSourceDraft, type LiveSourceInput} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from './lifetime';
export type UploadPreview = OwnerSourcePreview;
export type UploadSnapshot = Readonly<{draft: LiveSourceInput; reading: boolean; busy: boolean; pending?: boolean; preview?: UploadPreview; error?: unknown}>;
const empty: UploadSnapshot = Object.freeze({draft: Object.freeze({id: '', requestId: '', expectedRevision: 0, name: '', playlist: '', guide: '', tunerCount: 0}), reading: false, busy: false});
export class UploadSourceController extends OwnerStore<UploadSnapshot> {
  private draft = new LiveSourceDraft();
  private saving = false;
  private intent?: {body: SourceSave; id: string};
  private off: () => void;
  constructor(private api: LibraryContentApi, _serverId: string, isCurrent: () => boolean, reportError: (error: unknown) => void) {
    super(empty, isCurrent, reportError);this.draft.update({tunerCount: 1});this.snapshot = {...empty, draft: this.draft.getSnapshot()};
    this.off = this.draft.subscribe(() => {this.publish({...this.snapshot, draft: this.draft.getSnapshot(), reading: this.draft.isReading, preview: undefined});});
  }
  edit(patch: Pick<Partial<LiveSourceInput>, 'name' | 'tunerCount'>) {
    if (!this.isActive() || this.saving || this.intent) return;
    this.invalidate();this.draft.update(patch);this.publish({...this.snapshot, busy: false, error: undefined});
  }
  async read(field: 'playlist' | 'guide', read: () => Promise<string>) {
    if (!this.isActive() || this.saving || this.intent) return;
    const attempt = this.begin();this.publish({...this.snapshot, busy: false, preview: undefined, error: undefined});
    if (!this.valid(attempt)) return;
    try {
      // Draft.file itself publishes reading=true synchronously. Recheck inside
      // its reader too, before File.text or any caller-supplied I/O begins.
      await this.draft.file(field, () => {if (!this.valid(attempt)) throw {code: 'cancelled'};return read();});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});}
  }
  async check() {
    if (!this.isActive() || this.snapshot.busy || this.intent || this.draft.isReading || !this.snapshot.draft.name.trim() || (!this.snapshot.draft.playlist && !this.snapshot.draft.guide)) return;
    const attempt = this.begin(), draft = this.snapshot.draft;
    this.publish({...this.snapshot, busy: true, error: undefined, preview: undefined});
    if (!this.valid(attempt)) return;
    try {
      const raw = await previewOwnerSource(this.api, this.body(draft), attempt.abort.signal);
      if (this.valid(attempt)) this.publish({...this.snapshot, busy: false, preview: raw});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});}
  }
  private body(draft: LiveSourceInput): SourceSave {
    return {kind:draft.playlist?'playlist':'guide',name:draft.name.trim(),audience:'everyone',tunerCount:draft.tunerCount,
      ...(draft.playlist?{playlist:draft.playlist}:{}),...(draft.guide?{guide:draft.guide}:{})};
  }
  async save() {
    if (!this.isActive() || this.snapshot.busy || this.draft.isReading || !this.snapshot.preview) return false;
    const attempt=this.begin();this.saving=true;
    this.intent??={body:this.body(this.snapshot.draft),id:this.snapshot.draft.requestId};const intent=this.intent;
    this.publish({...this.snapshot,busy:true,pending:true,error:undefined});
    try {
      if(!this.valid(attempt))return false;
      await createOwnerSource(this.api,intent.body,intent.id,attempt.abort.signal);
      if(!this.valid(attempt))return false;
      this.intent=undefined;this.publish({...this.snapshot,busy:false,pending:false,preview:undefined});return true;
    } catch(error){if(this.valid(attempt)){if([400,401,403,409,412,422].includes(errorFacts(error).status??0))this.intent=undefined;this.fail(error,{...this.snapshot,busy:false,pending:!!this.intent,error:errorFacts(error)});}return false;}
    finally{this.saving=false;}
  }
  override dispose() {this.intent=undefined;super.dispose();this.off?.();this.draft?.dispose();}
}
