import type {LibraryContentApi} from '@core/library-content.ts';
import {lanConfirmationRoots, remoteSourceDraft, withConfirmedLanRoots, type RemoteSourceDraft, type RemoteSourcePreview} from '@core/linear-api.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {normalizeHdHomerunLocator} from '@core/server-admin/panel-logic.ts';
import {createOperationIds} from '@core/server-admin/server-forms.ts';
import {OwnerStore, errorFacts, terminal} from './lifetime';
import {confirmRoots, remotePreview, sourceReceipt} from './checked';
export type RemoteSourceSnapshot = Readonly<{draft: RemoteSourceDraft; busy: boolean; preview?: RemoteSourcePreview; confirm?: readonly string[]; error?: unknown}>;
export class RemoteSourceController extends OwnerStore<RemoteSourceSnapshot> {
  private saving = false;
  private operations: ReturnType<typeof createOperationIds>;
  constructor(private api: LibraryContentApi, private serverId: string, isCurrent: () => boolean,
    reportError: (error: unknown) => void, makeId = channelRequestID, tuner?: string) {
    super({busy: false, draft: remoteSourceDraft('')}, isCurrent, reportError);
    this.operations = createOperationIds(makeId);
    this.snapshot = {busy: false, draft: {...remoteSourceDraft(makeId()), viewerAccess: 'server-members', ...(tuner ? {kind: 'hdhomerun', locator: tuner, useDiscoveredCapacity: true} : {})}};
  }
  edit(patch: Partial<Pick<RemoteSourceDraft, 'kind' | 'name' | 'locator' | 'guideUrl' | 'username' | 'password' | 'viewerAccess' | 'refreshMs'>>) {
    if (!this.isActive() || this.saving) return;
    this.invalidate();const draft = {...this.snapshot.draft, ...patch};
    if (['kind', 'locator', 'guideUrl', 'username', 'password'].some(key => key in patch)) draft.confirmedLanRoots = [];
    this.publish({draft, busy: false});
  }
  async check(confirm = false) {
    if (!this.isActive() || this.snapshot.busy) return;
    let draft = this.snapshot.draft;if (!draft.name.trim() || !draft.locator.trim()) return;
    const attempt = this.begin();
    try {
      if (confirm) {if (!this.snapshot.confirm?.length) return;draft = withConfirmedLanRoots(draft, confirmRoots([...new Set([...draft.confirmedLanRoots, ...this.snapshot.confirm])]));}
      if (draft.kind === 'hdhomerun') draft = {...draft, locator: normalizeHdHomerunLocator(draft.locator)};
      this.publish({draft, busy: true});if (!this.valid(attempt)) return;
      const raw = await this.api.request('/v1/admin/live-sources/remote/preview', 'POST', draft, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      this.publish({draft, busy: false, preview: remotePreview(raw, this.serverId)});
    } catch (error) {
      if (!this.valid(attempt)) return;
      if (terminal(error)) {this.fail(error, this.getSnapshot());return;}
      try {const roots = lanConfirmationRoots(error);if (roots) {this.publish({draft, busy: false, confirm: confirmRoots(roots)});return;}}
      catch {error = {code: 'invalid_response'};}
      this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});
    }
  }
  async save() {
    const preview = this.snapshot.preview;
    if (!this.isActive() || this.snapshot.busy || !preview) return false;
    if (Date.parse(preview.expiresAt) <= Date.now()) {this.publish({...this.snapshot, preview: undefined, error: {code: 'channel_revision_conflict'}});return false;}
    const attempt = this.begin(), sourceId = this.snapshot.draft.id;this.saving = true;
    this.publish({...this.snapshot, busy: true, error: undefined});
    try {
      if (!this.valid(attempt)) return false;
      const receipt = await this.api.request('/v1/admin/live-sources/remote/save', 'POST', {previewId: preview.previewId, requestId: this.operations.forPayload(preview.previewId)}, attempt.abort.signal);
      if (!this.valid(attempt)) return false;
      sourceReceipt(receipt, this.serverId, sourceId);
      this.operations.release();this.publish({draft: remoteSourceDraft(''), busy: false});return this.valid(attempt);
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, busy: false, error: errorFacts(error)});return false;}
    finally {this.saving = false;}
  }
  override dispose() {this.operations?.release();super.dispose();}
}
