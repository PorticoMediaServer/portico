import type {HostedSession} from '../../../../../packages/client-core/src';
import type {CredentialSnapshot} from '../../../../../packages/client-core/src/credentials';

export type TVApprovalCredentials = Readonly<{
  synchronize: () => Promise<unknown>; getSnapshot: () => CredentialSnapshot;
  subscribe: (listener: () => void) => () => void;
}>;

/** A signed-out opening is spent permanently by an intervening foreign family,
 * even if that family signs out before a retained Retry reaches the screen. */
export function createTVApprovalCredentialOpening(credentials: TVApprovalCredentials, options: Readonly<{
  current: () => boolean; retired?: () => void;
}>) {
  let retired = false, seenOwn = false, expected: Readonly<{accountId: string; familyId: string}> | undefined;
  let off: (() => void) | undefined;
  const retire = () => {retired = true; const release = off; off = undefined; release?.();};
  const observe = () => {
    if (retired) return;
    if (!options.current() || retired) {retire(); return;}
    const value = credentials.getSnapshot().session;
    if (retired) return;
    if (!options.current() || retired) {retire(); return;}
    if (value && expected && value.account.id === expected.accountId && value.familyId === expected.familyId) {seenOwn = true; return;}
    if (value || seenOwn) {retire(); if (options.current()) options.retired?.();}
  };
  const release = credentials.subscribe(observe);
  if (retired) release(); else off = release;
  observe();
  return Object.freeze({
    current: () => !retired,
    allowAttempt(session: HostedSession) {
      if (retired || !options.current() || retired) return false;
      if (expected && (expected.accountId !== session.account.id || expected.familyId !== session.familyId)) {retire(); return false;}
      expected ??= Object.freeze({accountId: session.account.id, familyId: session.familyId});
      return !retired;
    },
    retire,
  });
}
export type TVApprovalCredentialOpening = ReturnType<typeof createTVApprovalCredentialOpening>;
