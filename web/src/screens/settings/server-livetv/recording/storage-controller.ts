import type {LibraryContentApi} from '@core/library-content.ts';
import {DVRClient, validStoragePolicy, type RecordingStorage, type StoragePolicy} from '@core/dvr.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
type StorageSnapshot = Readonly<{loading: boolean; saving: boolean; storage?: RecordingStorage; draft?: StoragePolicy; error?: unknown}>;
/** The existing DVR client owns parsing and stable write receipts. The owner
 * page adds only a local autosaved draft, never a second storage policy model. */
export class RecordingStorageController extends OwnerStore<StorageSnapshot> {
  private client: DVRClient;
  private saveTimer?: ReturnType<typeof setTimeout>;
  constructor(api: LibraryContentApi, serverId: string, current: () => boolean, reportError: (error: unknown) => void) {
    super(Object.freeze({loading: false, saving: false}), current, reportError);
    this.client = new DVRClient(api, serverId, async () => channelRequestID());
  }
  async load() {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving) return;
    const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const storage = await this.client.storage();if (!this.valid(attempt)) return;
      this.publish({loading: false, saving: false, storage, draft: storage.policy});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
  edit(patch: Partial<Omit<StoragePolicy, 'revision'>>) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || !this.snapshot.draft) return;
    const draft = {...this.snapshot.draft, ...patch};if (!validStoragePolicy(draft) || JSON.stringify(draft) === JSON.stringify(this.snapshot.draft)) return;
    this.publish({...this.snapshot, draft, error: undefined});
    clearTimeout(this.saveTimer);this.saveTimer = setTimeout(() => {this.saveTimer = undefined;void this.save();}, 500);
  }
  discard() {clearTimeout(this.saveTimer);this.saveTimer = undefined;if (this.isActive() && !this.snapshot.saving && this.snapshot.storage) this.publish({...this.snapshot, draft: this.snapshot.storage.policy, error: undefined});}
  async save() {
    clearTimeout(this.saveTimer);this.saveTimer = undefined;
    const draft = this.snapshot.draft;
    if (!this.isActive() || this.snapshot.saving || this.snapshot.loading || !draft || JSON.stringify(draft) === JSON.stringify(this.snapshot.storage?.policy)) return;
    const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const policy = await this.client.saveStorage(draft);if (!this.valid(attempt)) return;
      this.publish({...this.snapshot, saving: false, storage: this.snapshot.storage && {...this.snapshot.storage, policy}, draft: policy});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, saving: false, error: errorFacts(error)});}
  }
  override dispose() {clearTimeout(this.saveTimer);super.dispose();this.client?.dispose();}
}
