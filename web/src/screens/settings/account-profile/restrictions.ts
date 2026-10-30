import {IdentityClient} from '@core/identity-client.ts';
import type {ProfileAPI} from '@core/profile-management.ts';
import type {ProfileRestrictions, RatingSystem} from '@core/identity-restrictions.ts';
import {CodedError} from '@core/server-messages.ts';
import type {ProfileSettingsSource} from './source';

type Snapshot = Readonly<{loading: boolean; busy: boolean; document?: ProfileRestrictions; systems?: readonly RatingSystem[]; error?: unknown}>;
const empty: Snapshot = Object.freeze({loading: true, busy: false});
/** A selected profile's exact policy document, never a client-side content filter. */
export class ProfileRestrictionsSource {
  private readonly lifetime = new AbortController();
  private readonly listeners = new Set<() => void>();
  private state: Snapshot = empty;
  private epoch = 0;
  private closed = false;
  private readonly off: () => void;
  private selectedId?: string;
  constructor(private readonly api: ProfileAPI, private readonly profiles: ProfileSettingsSource, private readonly isCurrent: () => boolean) {
    this.off = profiles.subscribe(this.selectionChanged);this.selectionChanged();
  }
  private current = () => !this.closed && this.isCurrent() && !!this.profiles.getSnapshot().document;
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: Snapshot) {if (!this.current()) return;this.state = Object.freeze(state);for (const listener of this.listeners) listener();}
  private guardedClient(admitted: () => boolean): IdentityClient {
    return new IdentityClient({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!admitted()) throw new CodedError('forbidden', 'The selected profile is no longer available.');
      const result = await this.api.request<T>(path, method, body, signal ?? this.lifetime.signal);
      if (!admitted()) throw new CodedError('forbidden', 'The selected profile is no longer available.');
      return result;
    }});
  }
  private selectionChanged = () => {
    const id = this.profiles.selected()?.id;
    if (id === this.selectedId) return;
    this.selectedId = id;const epoch = ++this.epoch;
    if (!id || !this.current()) {this.state = empty;return;}
    const admitted = () => this.current() && epoch === this.epoch && this.profiles.selected()?.id === id;
    const client = this.guardedClient(admitted);
    this.publish(empty);
    void Promise.all([client.restrictions(id, this.lifetime.signal), client.ratingSystems(this.lifetime.signal)]).then(([document, systems]) => {
      if (!admitted()) return;
      if (document.profileId !== id) throw new CodedError('invalid_response', 'The profile limits could not be read.');
      this.publish({loading: false, busy: false, document, systems});
    }).catch(error => {if (epoch === this.epoch) this.publish({loading: false, busy: false, error});});
  };
  save = async (changes: Parameters<IdentityClient['saveRestrictions']>[1]): Promise<void> => {
    const state = this.getSnapshot(), account = this.profiles.getSnapshot().document, selected = this.profiles.selected();
    if (!this.current() || state.busy || !state.document || !account?.canManage || !account.capabilities.manageAccount || !selected || selected.id !== state.document.profileId) {
      throw new CodedError('forbidden', 'Profile management requires the main profile.');
    }
    const captured = state.document, epoch = this.epoch, target = selected;
    const admitted = () => {
      if (!this.current() || epoch !== this.epoch || this.state.document !== captured) return false;
      const liveAccount = this.profiles.getSnapshot().document, liveTarget = this.profiles.selected();
      return liveAccount?.canManage === true && liveAccount.capabilities.manageAccount === true &&
        liveTarget?.id === target.id && liveTarget.revision === target.revision && liveTarget.id === captured.profileId;
    };
    const client = this.guardedClient(admitted);
    this.publish({...state, busy: true});
    try {
      if (!admitted()) throw new CodedError('forbidden', 'Profile management requires the main profile.');
      const document = await client.saveRestrictions(captured, changes);
      if (!admitted()) return;
      if (document.profileId !== captured.profileId) throw new CodedError('invalid_response', 'The profile limits could not be read.');
      this.publish({loading: false, busy: false, document, systems: state.systems});
    } catch (error) {if (epoch === this.epoch) this.publish({loading: false, busy: false, error});throw error;}
  };
  dispose = () => {this.closed = true;++this.epoch;this.off();this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
