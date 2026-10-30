import type {LocalSession} from '../../../../packages/client-core/src';
import {SessionRefreshError} from '../../../../packages/client-core/src/server-connections';
import {routeOrigin} from '../../../../packages/client-core/src/route-identity';
import type {SignInKind} from '../../../../packages/client-core/src/app-sessions';
import {NativeQueueStore} from './queue-storage';

export type NativeSignInHint = Readonly<{serverId: string; accountId: string; familyId: string; fingerprint: string; origin: string;
  serverName: string; username: string; signIn: SignInKind}>;
const key = (serverId: string, accountId: string) => 'accounts.sign-in-label.v1:' + serverId + ':' + accountId;
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value);
/** Display context only: no password, token or grant is retained in this record. */
export async function rememberNativeSignInHint(value: NativeSignInHint) {
  if (!text(value.serverName, 120) || !text(value.username, 64) || routeOrigin(value.origin) !== value.origin) return;
  await new NativeQueueStore().change(key(value.serverId, value.accountId), () => ({record: {...value}, value: undefined}));
}
export async function readNativeSignInHint(session: LocalSession, origin: string, fingerprint: string, signIn: SignInKind): Promise<NativeSignInHint | undefined> {
  return new NativeQueueStore().change(key(session.viewer.serverId, session.viewer.accountId), raw => {
    const v = raw as Partial<NativeSignInHint> | null;
    const matches = v && v.serverId === session.viewer.serverId && v.accountId === session.viewer.accountId
      && v.familyId === session.sessionFamilyId && v.origin === origin && v.fingerprint === fingerprint && v.signIn === signIn
      && text(v.serverName, 120) && text(v.username, 64);
    return {record: raw, value: matches ? Object.freeze({...v}) as NativeSignInHint : undefined};
  });
}
export class NativeSignInEnded extends Error {
  readonly hint: NativeSignInHint;
  constructor(hint: NativeSignInHint) {super('native_sign_in_ended'); this.hint = hint;}
}
/** Only an actual terminal credential refusal/expiry selects this form. Selection
 * replacement, storage damage, permission and route/identity failures keep their handling. */
export function nativeSignInEnded(error: unknown, hint?: NativeSignInHint, expiresAt?: string): NativeSignInEnded | undefined {
  if (hint && error instanceof SessionRefreshError && (error.code === 'refresh_refused'
    || error.code === 'refresh_unavailable' && Date.now() >= Date.parse(expiresAt ?? ''))) return new NativeSignInEnded(hint);
  return undefined;
}
