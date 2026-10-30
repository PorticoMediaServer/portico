import {IdentityClient} from '@core/identity-client.ts';
import type {ProfileAPI} from '@core/profile-management.ts';
import {CodedError} from '@core/server-messages.ts';
import type {ProfileSettingsSource} from './source';

/** Reuse the checked per-device revocation producer. Never call the global
 * account sweep: that endpoint also signs this device out. No guessed counts. */
export class ProfileOtherDeviceSignOut {
  private busy = false;
  private closed = false;
  private readonly lifetime = new AbortController();
  constructor(private readonly api: ProfileAPI, private readonly profiles: ProfileSettingsSource, private readonly isCurrent: () => boolean) {}
  private admitted = () => !this.closed && this.isCurrent() && this.profiles.getSnapshot().document?.canManage === true && this.profiles.getSnapshot().document?.capabilities.manageAccount === true;
  signOut = async (): Promise<number> => {
    if (this.busy || !this.admitted()) throw new CodedError('forbidden', 'Profile management requires the main profile.');
    this.busy = true;
    const client = new IdentityClient({request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
      if (!this.admitted()) throw new CodedError('forbidden', 'The account is no longer available.');
      const result = await this.api.request<T>(path, method, body, this.lifetime.signal);
      if (!this.admitted()) throw new CodedError('forbidden', 'The account is no longer available.');
      return result;
    }});
    try {
      const devices = await client.devices('');
      const current = devices.filter(device => device.current);
      if (current.length !== 1) throw new CodedError('conflict', 'This device could not be identified.');
      let ended = 0;
      for (const device of devices) {
        if (device.id === current[0]!.id || device.current || device.sessions === 0) continue;
        await client.signOutDevice(device.id);ended++;
      }
      return ended;
    } finally {this.busy = false;}
  };
  dispose = () => {this.closed = true;this.lifetime.abort();};
}
