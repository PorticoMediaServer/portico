// The new server's answers, built from the shapes the tests already hold.
import type {Viewer} from '../src/index.ts';
import {parseDirectSnapshot, type DirectSnapshot} from '../src/profile-management.ts';
import {parseServerSignIn, type ServerSignIn} from '../src/sign-in.ts';

/** `GET /v1/me` for a sign-in that acts as `viewer`. A sign-in with no profile has the role `account`. */
export function me(viewer: Partial<Viewer> & Pick<Viewer, 'accountId' | 'role'>) {
  return {account: {id: viewer.accountId, username: 'someone', role: viewer.role === 'account' ? 'member' : viewer.role},
    ...(viewer.profileId ? {profile: {id: viewer.profileId, name: 'Someone', color: 'blue', isPrimary: true, pinRequired: false}} : {}),
    role: viewer.role, canManageAccount: viewer.role !== 'account'};
}

// The same answers built from the account, profile and server descriptions the app tests hold.
type Described = {id: string; name: string; primary?: boolean; isPrimary?: boolean; art?: string; color?: string; pinRequired?: boolean};
type Account = {account: {id: string; username: string; role: string}; profiles: readonly Described[]; canManage?: boolean};
type Role = {role?: string; profileId?: string};

const profileRow = (profile: Described) => ({id: profile.id, name: profile.name, color: profile.color ?? profile.art ?? 'blue', isPrimary: profile.isPrimary ?? profile.primary === true, pinRequired: profile.pinRequired === true});

/** `GET /v1/me` and `GET /v1/profiles` for the described account. The sign-in is on the main profile
 * unless `as` says otherwise; `canManage: false` describes one that manages nothing. */
export function accountAnswer(path: string, described: unknown, as: Role = {}): unknown {
  const value = described as Account, main = value.profiles.find(profile => profile.isPrimary ?? profile.primary) ?? value.profiles[0]!;
  const current = value.profiles.find(profile => profile.id === as.profileId) ?? main, manages = value.canManage !== false && current === main;
  if (path === '/v1/profiles') return {items: value.profiles.map(profileRow)};
  if (path === '/v1/me') return {account: {id: value.account.id, username: value.account.username, role: value.account.role}, profile: profileRow(current),
    role: as.role ?? (manages ? value.account.role : 'member'), canManageAccount: manages};
  return undefined;
}
export const accountPath = (path: string) => path === '/v1/me' || path === '/v1/profiles';

/** `GET /v1/system` for the described server. */
export function systemAnswer(described: {id: string; name: string; version?: string; signIn?: string; setupRequired?: boolean}) {
  return {id: described.id, name: described.name, ...(described.version ? {version: described.version} : {}), apiMajor: 1, capabilities: [],
    setupRequired: described.setupRequired === true, signInMethods: [described.signIn === 'portico' ? 'portico' : 'password']};
}

/** A sign-in answer (`POST /v1/auth/sign-in` and the routes that answer the same way) for the described account and session. */
export function signInAnswer(described: unknown): unknown {
  const value = described as Account & {session: unknown; challenge?: unknown};
  if (value.challenge) return {challenge: value.challenge};
  return {session: value.session, account: {id: value.account.id, username: value.account.username, role: value.account.role}, profiles: value.profiles.map(profileRow)};
}

/** What the app knows about the described account, read the way it reads the server's answers. */
export function snapshotOf(described: unknown, expected: {serverId?: string; accountId?: string} = {}, readable = false, as: Role = {}): DirectSnapshot {
  const value = described as {serverId?: string};
  return parseDirectSnapshot({me: accountAnswer('/v1/me', described, as), profiles: accountAnswer('/v1/profiles', described)}, {...expected, serverId: expected.serverId ?? value.serverId}, readable);
}
/** What the app knows about the described server before sign-in. */
export const serverOf = (described: Parameters<typeof systemAnswer>[0]): ServerSignIn => parseServerSignIn(systemAnswer(described));
