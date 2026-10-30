import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import {routeBody} from '../../contracts/src/routes.ts';
import {PROFILE_ART, type ProfileArt} from './profile-management.ts';
import {parseProfileRestrictions, restrictionEdit, type ProfileRestrictions} from './identity-restrictions.ts';
import {AVATAR_MAX_BYTES, type IdentityUpload} from './identity-client.ts';
import type {ProfileAvatar} from './identity-restrictions.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';

/** One of another person's profiles, as People's Manage edits it: the same
 * facts Profile & account shows, plus its restrictions. */
export type MemberProfile = Readonly<{id: string; name: string; art: ProfileArt; primary: boolean; position: number; revision: number;
  pinRequired: boolean; pinRevision: number; allowedLibraries: readonly string[]; restrictions: ProfileRestrictions; avatar?: ProfileAvatar}>;
export type MemberProfilesApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const count = (v: unknown, min = 0): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= min;
const invalid = (): never => {throw new CodedError('invalid_response', unreadableServerResponse);};

export function parseMemberProfile(raw: unknown): MemberProfile {
  if (!object(raw) || !text(raw.id, 128) || !text(raw.name, 120) || !PROFILE_ART.includes(raw.art as ProfileArt) || typeof raw.primary !== 'boolean'
    || !count(raw.position) || !count(raw.revision, 1) || typeof raw.pinRequired !== 'boolean' || !count(raw.pinRevision)
    || !Array.isArray(raw.allowedLibraries) || raw.allowedLibraries.length > 256 || raw.allowedLibraries.some(id => !text(id, 128))) return invalid();
  const restrictions = parseProfileRestrictions((raw as {restrictions?: unknown}).restrictions);
  if (restrictions.profileId !== raw.id) invalid();
  let avatar: ProfileAvatar | undefined;
  if (raw.avatar !== undefined) {
    const a = raw.avatar;
    if (!object(a) || a.profileId !== raw.id || !count(a.version, 1) || typeof a.updatedAt !== 'string' || !Number.isFinite(Date.parse(a.updatedAt))
      || !['image/jpeg', 'image/png'].includes(a.sourceMime as string) || typeof a.url !== 'string' || a.url.length > 512) invalid();
    avatar = Object.freeze({...a}) as ProfileAvatar;
  }
  const r = raw as Record<string, unknown>;
  return Object.freeze({id: r.id as string, name: r.name as string, art: r.art as ProfileArt, primary: r.primary as boolean, position: r.position as number,
    revision: r.revision as number, pinRequired: r.pinRequired as boolean, pinRevision: r.pinRevision as number,
    allowedLibraries: Object.freeze([...(r.allowedLibraries as string[])]), restrictions, ...(avatar ? {avatar} : {})});
}

/**
 * Another person's profiles, bound to that one account. Every answer must name
 * that account or a profile already read for it; the server proves the
 * caller's tier on each request, and an edit is fenced on the revision read.
 */
export class MemberProfilesClient {
  constructor(private readonly api: MemberProfilesApi, readonly accountId: string, private readonly upload?: IdentityUpload) {
    if (!text(accountId, 128)) throw new Error('An account is required.');
  }
  async list(signal?: AbortSignal): Promise<readonly MemberProfile[]> {
    const raw = await this.api.request<unknown>(encodedRoutePath('get_admin_access_members_id_profiles', {id: this.accountId}), 'GET', undefined, signal);
    if (!object(raw) || raw.accountId !== this.accountId || !Array.isArray(raw.items) || raw.items.length > 64) invalid();
    const items = (raw as {items: unknown[]}).items.map(value => this.scoped(parseMemberProfile(value)));
    if (new Set(items.map(item => item.id)).size !== items.length || items.filter(item => item.primary).length > 1) invalid();
    return Object.freeze(items);
  }
  private scoped(profile: MemberProfile): MemberProfile {
    if (profile.avatar && profile.avatar.url !== encodedRoutePath('get_admin_access_members_id_profiles_profile_avatar', {id: this.accountId, profileId: profile.id}) + '?v=' + profile.avatar.version) invalid();
    return profile;
  }
  private same(profile: MemberProfile, next: MemberProfile): MemberProfile {
    if (next.id !== profile.id) invalid();
    return this.scoped(next);
  }
  async rename(profile: MemberProfile, change: Readonly<{name?: string; art?: ProfileArt}>, signal?: AbortSignal): Promise<MemberProfile> {
    const raw = await this.api.request<unknown>(encodedRoutePath('patch_admin_access_members_id_profiles_profile', {id: this.accountId, profileId: profile.id}), 'PATCH',
      routeBody('patch_admin_access_members_id_profiles_profile', {expectedRevision: profile.revision, ...change}), signal);
    return this.same(profile, parseMemberProfile(raw));
  }
  async restrict(profile: MemberProfile, changes: Parameters<typeof restrictionEdit>[1], signal?: AbortSignal): Promise<MemberProfile> {
    const raw = await this.api.request<unknown>(encodedRoutePath('put_admin_access_members_id_profiles_profile_restrictions', {id: this.accountId, profileId: profile.id}), 'PUT',
      restrictionEdit(profile.restrictions, changes), signal);
    return this.same(profile, parseMemberProfile(raw));
  }
  async uploadPicture(profile: MemberProfile, picture: Blob | ArrayBuffer | Uint8Array, contentType: string): Promise<MemberProfile> {
    if (!this.upload) throw new Error('This device can’t send pictures.');
    const size = typeof Blob !== 'undefined' && picture instanceof Blob ? picture.size : (picture as ArrayBuffer | Uint8Array).byteLength;
    if (!size || size > AVATAR_MAX_BYTES) throw new CodedError('invalid_request', 'Choose a picture smaller than 4 MB.');
    if (!['image/jpeg', 'image/png'].includes(contentType)) throw new CodedError('invalid_request', 'Choose a JPEG or PNG picture.');
    const path = encodedRoutePath('post_admin_access_members_id_profiles_profile_avatar', {id: this.accountId, profileId: profile.id}) + '?expectedRevision=' + profile.revision;
    const next = this.same(profile, parseMemberProfile(await this.upload(path, picture, contentType)));
    if (!next.avatar || next.revision <= profile.revision) invalid();
    return next;
  }
  async removePicture(profile: MemberProfile, signal?: AbortSignal): Promise<MemberProfile> {
    const raw = await this.api.request<unknown>(encodedRoutePath('delete_admin_access_members_id_profiles_profile_avatar', {id: this.accountId, profileId: profile.id}), 'DELETE',
      routeBody('delete_admin_access_members_id_profiles_profile_avatar', {expectedRevision: profile.revision}), signal);
    const next = this.same(profile, parseMemberProfile(raw));
    if (next.avatar || next.revision <= profile.revision) invalid();
    return next;
  }
  /** Clears the PIN so the person can choose a new one; nobody else chooses it for them. */
  async resetPIN(profile: MemberProfile, signal?: AbortSignal): Promise<MemberProfile> {
    const raw = await this.api.request<unknown>(encodedRoutePath('post_admin_access_members_id_profiles_profile_pin_reset', {id: this.accountId, profileId: profile.id}), 'POST',
      routeBody('post_admin_access_members_id_profiles_profile_pin_reset', {expectedRevision: profile.revision}), signal);
    const next = this.same(profile, parseMemberProfile(raw));
    if (next.pinRequired) invalid();
    return next;
  }
}
