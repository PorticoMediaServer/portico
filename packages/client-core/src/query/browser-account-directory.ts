import {listHostedServers} from './connection-operations.ts';
import {parseMembershipEndingNotice} from '../membership-ending.ts';
import {decodeServersListResponse} from '../../../contracts/src/hosted/decoders.ts';
import {routePath} from '../../../contracts/src/hosted/routes.ts';
import type {BrowserHostedAccount} from './hosted-account-transport.ts';
import {currentAccountRead, type AccountIdentity} from './browser-account-work.ts';

type AccountPort = Pick<BrowserHostedAccount, 'api' | 'service'>;
/** Inert binding over the caller's existing browserAccount singleton. No UI,
 * session/store construction, cache, guessed origin or Server admission. */
export function createAccountServerDirectory(account: AccountPort, scope: AccountIdentity) {
  const captured = Object.freeze({accountId: scope.accountId, familyId: scope.familyId});
  return {
    scope: captured,
    read: async (signal?: AbortSignal) => {
      signal?.throwIfAborted();
      const result = await currentAccountRead(account, async session => {
        signal?.throwIfAborted();
        const raw = await listHostedServers(account.api, signal, session.accessToken);
        signal?.throwIfAborted();
        // The additive field is parsed strictly here until the next official
        // generation, and stays narrowed after generation changes its TS shape.
        if (raw && typeof raw === 'object' && !Array.isArray(raw) && Object.hasOwn(raw, 'membershipEnding')) {
          const {membershipEnding, ...base} = raw as Record<string, unknown>;
          const result = decodeServersListResponse(base);
          const ending = parseMembershipEndingNotice(membershipEnding);
          if (result.items.length) throw new Error('Membership ending accompanied current servers.');
          return {...result, membershipEnding: ending};
        }
        return {...decodeServersListResponse(raw), membershipEnding: undefined};
      }, captured, signal);
      signal?.throwIfAborted();
      return result;
    },
  };
}

export type AccountServerDirectory = ReturnType<typeof createAccountServerDirectory>;
