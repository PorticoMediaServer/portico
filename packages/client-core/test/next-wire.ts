// The new server's answers, built from the shapes the tests already hold.
import type {Viewer} from '../src/index.ts';

/** `GET /v1/me` for a sign-in that acts as `viewer`. A sign-in with no profile has the role `account`. */
export function me(viewer: Partial<Viewer> & Pick<Viewer, 'accountId' | 'role'>) {
  return {account: {id: viewer.accountId, username: 'someone', role: viewer.role === 'account' ? 'member' : viewer.role},
    ...(viewer.profileId ? {profile: {id: viewer.profileId, name: 'Someone', color: 'blue', isPrimary: true, pinRequired: false}} : {}),
    role: viewer.role, canManageAccount: viewer.role !== 'account'};
}
