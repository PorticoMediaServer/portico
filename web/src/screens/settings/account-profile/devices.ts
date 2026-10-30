import {IdentityClient} from '@core/identity-client.ts';
import type {Device} from '@core/identity-devices.ts';
import type {ProfileAPI} from '@core/profile-management.ts';
import {CodedError} from '@core/server-messages.ts';
import type {ProfileSettingsSource} from './source';
type Snapshot = Readonly<{loading: boolean; busy: boolean; devices?: readonly Device[]; error?: unknown}>;
const empty: Snapshot = Object.freeze({loading: true, busy: false});
export class ProfileDevicesSource {
  private state: Snapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private closed = false;
  private pending = false;
  private readonly client: IdentityClient;
  constructor(api: ProfileAPI, private readonly profiles: ProfileSettingsSource, private readonly isCurrent: () => boolean) {
    this.client = new IdentityClient({request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
      if (!this.admitted()) throw new CodedError('forbidden', 'Account management requires the main profile.');
      const result = await api.request<T>(path, method, body, this.lifetime.signal);
      if (!this.admitted()) throw new CodedError('forbidden', 'Account management requires the main profile.');
      return result;
    }});
  }
  private admitted = () => !this.closed && this.isCurrent() && this.profiles.getSnapshot().document?.canManage === true && this.profiles.getSnapshot().document?.capabilities.manageAccount === true;
  getSnapshot = () => this.admitted() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: Snapshot) {if (!this.admitted()) return;this.state = Object.freeze(state);for (const listener of this.listeners) listener();}
  load = async () => {
    if (!this.admitted() || this.pending || this.state.devices) return;
    this.pending = true;
    try {const devices = await this.client.devices('');this.publish({loading: false, busy: false, devices});}
    catch (error) {this.publish({loading: false, busy: false, error});}
    finally {this.pending = false;}
  };
  signOut = async (captured: Device): Promise<void> => {
    if (!this.admitted() || this.pending || this.state.busy || !this.state.devices?.includes(captured)) throw new CodedError('conflict', 'Refresh the devices before trying again.');
    this.pending = true;this.publish({...this.state, busy: true});
    try {
      await this.client.signOutDevice(captured.id);
      if (captured.current) return;
      const devices = await this.client.devices('');this.publish({loading: false, busy: false, devices});
    } catch (error) {this.publish({loading: false, busy: false, error});throw error;}
    finally {this.pending = false;}
  };
  dispose = () => {this.closed = true;this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
