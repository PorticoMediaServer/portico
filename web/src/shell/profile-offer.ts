import type {ShellIdentity} from './Shell';
import type {ProfileIdentity} from '../kit/shell';

/** Display names, artwork, ordering, unread and library labels are not opening proof. */
export function profileTargetKey(profile: ProfileIdentity | undefined): string {
  return JSON.stringify(profile ? [profile.id, profile.revision, profile.pinRevision, profile.trustRevision, profile.pinRequired === true] : null);
}
export function switchAuthorityKey(identity: ShellIdentity): string {
  const viewer = identity.viewer;
  return JSON.stringify([viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role, identity.signIn,
    identity.primaryProfile, identity.canManageProfiles, identity.capabilities]);
}
export function switchOfferKey(identity: ShellIdentity): string {
  return JSON.stringify([switchAuthorityKey(identity), identity.profiles.map(profileTargetKey).sort()]);
}
