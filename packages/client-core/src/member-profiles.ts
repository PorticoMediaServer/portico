import {PROFILE_ART, parseManagedProfiles, type ProfileArt} from './profile-management.ts';
import {readRestrictions, saveRestrictions, type ProfileRestrictions, type RestrictionChanges} from './profile-restrictions.ts';
import type {IdentityUpload} from './identity-client.ts';
import type {ProfileAvatar} from './identity-restrictions.ts';
import {callServer} from './query/server-api.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';

/** One of another person's profiles, as People's Manage edits it: the same facts Profile & account
 * shows, plus its limits. `revision` and `pinRevision` are always 1: the later save wins. */
export type MemberProfile = Readonly<{id: string; name: string; art: ProfileArt; primary: boolean; position: number; revision: number;
  pinRequired: boolean; pinRevision: number; allowedLibraries: readonly string[]; restrictions: ProfileRestrictions; avatar?: ProfileAvatar}>;
export type MemberProfilesApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const invalid = (): never => {throw new CodedError('invalid_response', unreadableServerResponse);};
const notYet = (): never => {throw new CodedError('not_available', 'This is not available on this server yet.');};

/**
 * Another person's profiles, for the owner or an administrator (`?member=<account id>` on the profile routes).
 * The server checks on every request that the caller may manage that person.
 */
export class MemberProfilesClient {
  constructor(private readonly api: MemberProfilesApi, readonly accountId: string, _upload?: IdentityUpload) {
    if (!text(accountId, 128)) throw new Error('An account is required.');
  }
  async list(signal?: AbortSignal): Promise<readonly MemberProfile[]> {
    const profiles = parseManagedProfiles((await callServer(this.api, 'GET /v1/profiles', {query: {member: this.accountId}, signal}))?.items, true);
    return Object.freeze(await Promise.all(profiles.map(async profile => {
      const restrictions = await readRestrictions(this.api, profile.id, this.accountId, signal);
      return Object.freeze({id: profile.id, name: profile.name, art: profile.art, primary: profile.primary, position: profile.position, revision: 1, pinRequired: profile.pinRequired,
        pinRevision: 1, allowedLibraries: restrictions.allLibraries ? Object.freeze([]) : restrictions.libraries, restrictions});
    })));
  }
  private async reread(profile: MemberProfile, signal?: AbortSignal): Promise<MemberProfile> {
    const next = (await this.list(signal)).find(candidate => candidate.id === profile.id);
    if (!next) invalid();
    return next!;
  }
  async rename(profile: MemberProfile, change: Readonly<{name?: string; art?: ProfileArt}>, signal?: AbortSignal): Promise<MemberProfile> {
    if (change.art !== undefined && !PROFILE_ART.includes(change.art)) invalid();
    await callServer(this.api, 'PATCH /v1/profiles/{id}', {id: profile.id, query: {member: this.accountId}, body: {...(change.name === undefined ? {} : {name: change.name.trim()}), ...(change.art === undefined ? {} : {color: change.art})}, signal});
    return this.reread(profile, signal);
  }
  async restrict(profile: MemberProfile, changes: RestrictionChanges, signal?: AbortSignal): Promise<MemberProfile> {
    const restrictions = await saveRestrictions(this.api, profile.restrictions, changes, this.accountId, signal);
    return Object.freeze({...profile, restrictions, allowedLibraries: restrictions.allLibraries ? Object.freeze([]) : restrictions.libraries});
  }
  /** Profile pictures are not on the rebuilt server yet. */
  async uploadPicture(_profile: MemberProfile, _picture: Blob | ArrayBuffer | Uint8Array, _contentType: string): Promise<MemberProfile> {return notYet();}
  async removePicture(_profile: MemberProfile, _signal?: AbortSignal): Promise<MemberProfile> {return notYet();}
  /** Removes the PIN so the person can choose a new one; nobody else chooses it for them. */
  async resetPIN(profile: MemberProfile, signal?: AbortSignal): Promise<MemberProfile> {
    await callServer(this.api, 'DELETE /v1/profiles/{id}/pin', {id: profile.id, query: {member: this.accountId}, signal});
    return Object.freeze({...profile, pinRequired: false});
  }
}
