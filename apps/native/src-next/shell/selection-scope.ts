import type {LocalSession} from '../../../../packages/client-core/src';
import type {SelectionContext} from '../../../../packages/client-core/src/selection-model/index';

/** Keep the actual session principal; unknown authority strings never acquire local access. */
export function nativeHomeSelectionScope(viewer: LocalSession['viewer']): Omit<SelectionContext['scope'], 'privacyFence'> {
  const authority = viewer.authority;
  if (authority !== 'local' && authority !== 'hosted') throw Object.assign(new Error('invalid_home_authority'), {code: 'invalid_response'});
  return {serverId: viewer.serverId, authority, accountId: viewer.accountId, profileId: viewer.profileId, viewId: 'home'};
}
