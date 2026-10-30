import type {ContentScope} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';

/** Search, Saved and Library own their producer fences. Home's response fence
 * is only one part of the actual authority lifetime, never their wire scope. */
export function nativeDestinationLifetime(context: NativeShellBootstrapContext) {
  const viewer = context.session()?.viewer, authority = context.authority();
  if (!viewer || !context.current() || viewer.serverId !== authority.serverId || viewer.accountId !== authority.accountId || viewer.profileId !== authority.profileId) throw new Error('native_destination_authority_required');
  const scope: ContentScope = Object.freeze({serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])});
  return {scope, current: () => {
    const live = context.session()?.viewer;
    return context.current() && sameAuthority(authority, context.authority()) && !!live && live.authority === viewer.authority
      && live.serverId === viewer.serverId && live.accountId === viewer.accountId && live.profileId === viewer.profileId;
  }};
}
