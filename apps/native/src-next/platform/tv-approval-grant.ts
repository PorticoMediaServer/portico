import type {HostedSession} from '../../../../packages/client-core/src';
import {parseAccountSession} from '../../../../packages/client-core/src/hosted-security';

/** An approved native grant is a credential, never a live mutable DTO. Preserve
 * every validated scalar before verification, and keep the source alias as a
 * refusal witness rather than as the credential that can be admitted. */
export function captureTVApprovalGrant(source: HostedSession) {
  const parsed = parseAccountSession(source, 'native');
  const session: HostedSession = Object.freeze({...parsed, account: Object.freeze({...parsed.account})});
  const current = () => {
    try {
      return source.account.id === session.account.id && source.account.username === session.account.username
        && source.account.displayName === session.account.displayName && source.familyId === session.familyId
        && source.accessToken === session.accessToken && source.refreshToken === session.refreshToken
        && source.expiresAt === session.expiresAt && source.refreshExpiresAt === session.refreshExpiresAt;
    } catch {return false;}
  };
  return Object.freeze({session, current});
}
