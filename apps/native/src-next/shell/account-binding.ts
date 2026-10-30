import type {LocalSession} from '../../../../packages/client-core/src';
import type {AppServerSession} from '../../../../packages/client-core/src/app-sessions';
import type {ServerPin} from '../../../../packages/client-core/src/route-identity';
import type {DirectAccount, DirectSnapshot} from '../../../../packages/client-core/src/profile-management';

/** The role a server issues to a profile's session: the account's own role for its primary
 * profile, "member" for every other profile (server identity/direct_profiles.go). Comparing a
 * session's role with the account's role alone refuses every non-primary profile (E-mobile-01/25). */
export function profileSessionRole(account: Pick<DirectAccount, 'role' | 'primaryProfileId'>, profileId: string): string {
  return profileId === account.primaryProfileId ? account.role : 'member';
}

const cancelled = () => Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});
const changed = () => Object.assign(new Error('native_shell_account_binding_changed'), {code: 'identity_mismatch'});

export type NativeShellAccountBinding = Readonly<{
  /** Identity linkage only. Roles, profiles and menus still come from this server. */
  hostedAccountId?: string;
  current: () => boolean;
  assertCurrent: () => Promise<void>;
  assertAccount: (snapshot: DirectSnapshot) => void;
  /** Identity only: the same server, account and Portico linkage, not disabled. For a snapshot
   * read under another profile's session of this account (a profile switch), whose account role
   * the server reports as that session's role (E-tv-41). The caller checks the new session's role. */
  assertSameAccount: (snapshot: DirectSnapshot) => void;
}>;

/** Portico sign-in issues a local server session. Bind its real account snapshot to
 * the original protected connection, without contacting or depending on Hosted. */
export function createNativeShellAccountBinding(selected: AppServerSession, session: LocalSession, options: {
  current: () => boolean;
  session: () => LocalSession | undefined;
  pin: () => ServerPin | undefined;
  readSelected: () => Promise<AppServerSession | undefined>;
}): NativeShellAccountBinding {
  const connection = selected.connection;
  const pin = Object.freeze({...connection.pin}), viewer = Object.freeze({...session.viewer});
  const family = session.sessionFamilyId, horizon = session.authorizationHorizon;
  const device = session.deviceId, installation = session.installationId;
  const minimumGeneration = BigInt(session.tokenGeneration);
  const signIn = selected.signIn, resetOwner = selected.resetFileOwner;
  const origin = connection.logicalOrigin, selectionId = connection.selectionId;
  const hosted = connection.hosted && Object.freeze({origin: connection.hosted.origin, accountId: connection.hosted.accountId});
  const samePin = (value: ServerPin | undefined) => !!value && value.serverId === pin.serverId
    && value.publicKey === pin.publicKey && value.fingerprint === pin.fingerprint;
  const sameSession = (value: LocalSession | undefined) => !!value && value.viewer.authority === 'local'
    && value.sessionFamilyId === family && value.authorizationHorizon === horizon
    && value.deviceId === device && value.installationId === installation
    && (['serverId', 'accountId', 'profileId', 'authority', 'role'] as const).every(key => value.viewer[key] === viewer[key])
    && BigInt(value.tokenGeneration) >= minimumGeneration;
  const sameSelection = (value: AppServerSession | undefined) => !!value && value.signIn === signIn
    && value.resetFileOwner === resetOwner && value.connection.logicalOrigin === origin
    && value.connection.selectionId === selectionId && samePin(value.connection.pin) && sameSession(value.connection.session)
    && (hosted ? value.connection.hosted?.origin === hosted.origin && value.connection.hosted.accountId === hosted.accountId : !value.connection.hosted);
  const current = () => options.current() && samePin(options.pin()) && sameSession(options.session());
  const sameAccount = (snapshot: DirectSnapshot) => {
    if (!current()) throw cancelled();
    if (snapshot.authority !== 'local' || snapshot.serverId !== viewer.serverId || snapshot.account.id !== viewer.accountId
      || snapshot.account.disabled || hosted && snapshot.account.hostedAccountId !== hosted.accountId) throw changed();
  };
  return Object.freeze({...(hosted ? {hostedAccountId: hosted.accountId} : {}), current,
    assertCurrent: async () => {
      if (!current()) throw cancelled();
      const latest = await options.readSelected();
      if (!current()) throw cancelled();
      if (!sameSelection(latest)) throw changed();
    },
    assertAccount: (snapshot: DirectSnapshot) => {
      sameAccount(snapshot);
      if (profileSessionRole(snapshot.account, viewer.profileId) !== viewer.role) throw changed();
    },
    assertSameAccount: (snapshot: DirectSnapshot) => {sameAccount(snapshot);},
  });
}
