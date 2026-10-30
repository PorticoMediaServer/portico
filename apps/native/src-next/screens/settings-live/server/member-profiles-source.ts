import {MemberProfilesClient, type MemberProfile} from '../../../../../../packages/client-core/src/member-profiles';
import {IdentityClient} from '../../../../../../packages/client-core/src/identity-client';
import type {ProfileRestrictions, RatingSystem} from '../../../../../../packages/client-core/src/identity-restrictions';
import type {ProfileArt} from '../../../../../../packages/client-core/src/profile-management';
import type {AccessMember} from '../../../../../../packages/client-core/src/server-administration';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {ArtworkSource} from '../../../kit/artwork/types';
import {pickProfilePicture, profileAvatarUpload, profilePicturePickerAvailable, profilePictureSource} from '../../../platform/profile-avatars';
import {NativeScreenLifetime, screenScopeEnded} from '../lifetime';

export type PINConfirmation = Readonly<{profile: MemberProfile}>;
type Snapshot = Readonly<{items?: readonly MemberProfile[]; systems: readonly RatingSystem[]; loading: boolean; busy: boolean; managing?: MemberProfile; pinConfirmation?: PINConfirmation; error?: unknown; changed?: boolean; denied?: boolean}>;
const initial: Snapshot = Object.freeze({systems: [], loading: false, busy: false});
export type RestrictionChanges = Partial<Omit<ProfileRestrictions, 'profileId' | 'revision' | 'maximumAge'>>;
/** A second fence before calling the server. Successful GET is also required:
 * this server's read and command routes prove the same management tier. */
export function memberProfileTier(caller: string, target: string): boolean {
  return caller === 'owner' && (target === 'admin' || target === 'member') || caller === 'admin' && target === 'member';
}

/** One committed Profiles tab for one exact person revision. Its parsed rows
 * are the only command witnesses; neither ids nor equal-looking copies work. */
export class NativeMemberProfilesSource {
  private state = initial;
  private lifetime: NativeScreenLifetime;
  private listeners = new Set<() => void>();
  private flight?: AbortController;
  private mutation?: AbortController;
  private client: MemberProfilesClient;
  private identity: IdentityClient;
  constructor(readonly context: NativeShellBootstrapContext, readonly person: AccessMember, private personCurrent: () => boolean, private reportError: (error: unknown) => void) {
    this.lifetime = new NativeScreenLifetime(context);
    const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => this.current()
      ? this.lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
    const upload = profileAvatarUpload({requestRaw: (path, method, options) => {
      const mutation = this.mutation;
      if (!this.current() || !this.tier() || !mutation || mutation.signal.aborted) return Promise.reject(screenScopeEnded());
      return this.lifetime.request(signal => context.api.requestRaw(path, method, {...options, signal}), mutation.signal);
    }});
    this.client = new MemberProfilesClient(api, person.accountId, upload);
    this.identity = new IdentityClient(api);
  }
  current = () => this.lifetime.current() && this.personCurrent();
  private tier = () => memberProfileTier(this.context.account.account.role, this.person.role) && this.context.account.capabilities.managePeople
    && this.person.accountId !== this.context.account.account.id;
  getSnapshot = (): Snapshot => this.current() ? this.state : initial;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<Snapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch}); for (const listener of this.listeners) listener();}}
  private fail(error: unknown) {
    if ((error as {status?: number})?.status === 401) {this.dispose(); this.reportError(error);}
    else if ((error as {status?: number})?.status === 403) this.publish({items: undefined, managing: undefined, pinConfirmation: undefined, denied: true, error});
    else this.publish({error});
  }
  canManage = (profile: MemberProfile): boolean => this.current() && this.tier() && !this.state.denied && !this.state.loading && !this.state.busy && !!this.state.items?.includes(profile);
  canPickPicture = (profile: MemberProfile): boolean => this.canManage(profile) && profilePicturePickerAvailable();
  picture = (profile: MemberProfile, pixels: number): ArtworkSource | undefined => {
    if (!this.current() || !this.tier() || this.state.denied || !this.state.items?.includes(profile)) return undefined;
    const avatar = profile.avatar;
    // This surface must never fall back to the public profile-picture route.
    const expected = `/v1/admin/access/members/${encodeURIComponent(this.person.accountId)}/profiles/${encodeURIComponent(profile.id)}/avatar?v=${avatar?.version}`;
    if (!avatar || avatar.profileId !== profile.id || avatar.url !== expected) return undefined;
    const picture = profilePictureSource(this.context.api, this.context.session()?.accessToken, avatar, pixels);
    return picture && typeof picture === 'object' && 'uri' in picture && typeof picture.uri === 'string'
      ? {uri: picture.uri, cacheKey: JSON.stringify(['member-avatar', this.lifetime.authority, this.person.accountId, profile.id, avatar.version, picture.uri]),
        ...(picture.headers ? {headers: picture.headers as Readonly<Record<string, string>>} : {})}
      : undefined;
  };
  load = async (): Promise<void> => {
    if (!this.current() || this.state.busy) return;
    this.flight?.abort(); const flight = this.flight = new AbortController();
    if (!this.tier()) {this.flight = undefined; this.publish({...initial, denied: true}); return;}
    const owns = () => this.current() && this.flight === flight && !flight.signal.aborted;
    this.publish({loading: true, items: undefined, managing: undefined, pinConfirmation: undefined, error: undefined, changed: false, denied: false});
    try {
      const items = await this.client.list(flight.signal);
      if (!owns()) return;
      this.publish({items});
      const systems = await this.identity.ratingSystems(flight.signal);
      if (owns()) this.publish({systems});
    } catch (error) {if (owns()) this.fail(error);}
    finally {if (owns()) this.publish({loading: false}); if (this.flight === flight) this.flight = undefined;}
  };
  manage = (profile: MemberProfile) => {if (this.canManage(profile)) this.publish({managing: profile, pinConfirmation: undefined, error: undefined, changed: false});};
  close = () => {if (this.current() && !this.state.busy) this.publish({managing: undefined, pinConfirmation: undefined});};
  askRemovePIN = (profile: MemberProfile) => {if (this.canManage(profile) && this.state.managing === profile && profile.pinRequired) this.publish({pinConfirmation: Object.freeze({profile})});};
  cancelRemovePIN = () => {if (this.current() && !this.state.busy) this.publish({pinConfirmation: undefined});};
  private async change(profile: MemberProfile, work: (signal: AbortSignal) => Promise<MemberProfile | undefined>): Promise<boolean> {
    if (!this.canManage(profile) || this.state.managing !== profile) return false;
    const flight = this.mutation = new AbortController();
    const owns = () => this.current() && this.tier() && !this.state.denied && this.mutation === flight && !flight.signal.aborted && this.state.managing === profile && !!this.state.items?.includes(profile);
    this.publish({busy: true, pinConfirmation: undefined, error: undefined, changed: false});
    try {
      const next = await work(flight.signal);
      if (!owns() || !next) return false;
      this.publish({items: this.state.items!.map(item => item === profile ? next : item), managing: next});
      return true;
    } catch (error) {
      if (!owns()) return false;
      const failure = error as {status?: number; code?: string};
      if (failure.status === 409 || /(^|_)(conflict|changed|stale|revision)(_|$)/.test(failure.code ?? '')) {
        // Never retry a rejected edit against a new revision. Replace the whole
        // read, discard the draft and confirmation, then ask the person to review.
        this.publish({items: undefined, managing: undefined, loading: true, changed: true});
        try {const items = await this.client.list(flight.signal); if (this.current() && this.mutation === flight && !flight.signal.aborted) this.publish({items, managing: items.find(item => item.id === profile.id)});}
        catch (readError) {if (this.current() && this.mutation === flight && !flight.signal.aborted) this.fail(readError);}
      } else this.fail(error);
      return false;
    } finally {
      if (this.mutation === flight) {this.mutation = undefined; this.publish({busy: false, loading: false});}
    }
  }
  rename = (profile: MemberProfile, name: string) => this.change(profile, signal => this.client.rename(profile, {name: name.trim()}, signal));
  colour = (profile: MemberProfile, art: ProfileArt) => this.change(profile, signal => this.client.rename(profile, {art}, signal));
  restrict = (profile: MemberProfile, changes: RestrictionChanges) => this.change(profile, signal => this.client.restrict(profile, changes, signal));
  choosePicture = (profile: MemberProfile): Promise<boolean> => {
    if (!this.canPickPicture(profile)) return Promise.resolve(false);
    return this.change(profile, async signal => {
      const current = () => this.current() && this.tier() && !signal.aborted && this.state.managing === profile && !!this.state.items?.includes(profile);
      const picked = await pickProfilePicture({current});
      if (!picked || !current()) return undefined;
      return this.client.uploadPicture(profile, picked.bytes, picked.contentType);
    });
  };
  removePicture = (profile: MemberProfile): Promise<boolean> => profile.avatar
    ? this.change(profile, signal => this.client.removePicture(profile, signal)) : Promise.resolve(false);
  removePIN = (profile: MemberProfile, confirmation?: PINConfirmation): Promise<boolean> => !!confirmation && this.state.pinConfirmation === confirmation && confirmation.profile === profile && profile.pinRequired
    ? this.change(profile, signal => this.client.resetPIN(profile, signal)) : Promise.resolve(false);
  dispose = () => {this.flight?.abort(); this.mutation?.abort(); this.flight = undefined; this.mutation = undefined; this.lifetime.dispose(); this.state = initial; this.listeners.clear();};
}
