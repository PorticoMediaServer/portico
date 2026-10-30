import {parseMembershipEndingNotice} from '@core/membership-ending.ts';
import {decodeServersListResponse} from '../../../packages/contracts/src/hosted/decoders.ts';
import {routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import type {browserAccount} from './account';
import {currentAccountRead, type AccountIdentity} from './account-work';

type AccountPort = Pick<ReturnType<typeof browserAccount>, 'api' | 'service'>;
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
        const raw = await account.api.send(routePath('servers_list'), 'GET', undefined, signal, session.accessToken);
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
