import type {AccountSecurityPending, OriginalSecurityAccount} from '../../src/platform/account-security-journal.ts';

// TEST-ONLY canonical DTO fixture for AccountSecurityJournal, derived exactly from
// the parser's accepted shape. It is data-parser input, never provider or sign-in
// authentication, and it carries no credential or accepting authority.
const NOW = Date.now();
export const iso = (offset: number) => new Date(NOW + offset).toISOString().replace(/\.\d{3}Z$/, 'Z');
export const capturedNow = () => NOW;

export function hostedFixture() {
  const session = {
    account: {id: 'acct_fixture', username: 'fixture', displayName: 'Fixture'},
    familyId: 'family_fixture',
    accessToken: 'a'.repeat(43),
    expiresAt: iso(600000),
    refreshExpiresAt: iso(3600000),
  };
  const state = {
    accountId: session.account.id, familyId: session.familyId,
    contactAddress: 'fixture@example.test', privateEmail: false, hasPassword: false,
    mfaEnabled: false, recoveryCodesRemaining: 0,
    providers: [{id: 'identity_fixture', provider: 'google' as const, revision: '1'}],
  };
  const coordinator = JSON.stringify({version: 1, revocations: [], active: {session: {...session, accessToken: ''}, authority: {familyId: session.familyId}, context: {}}});
  const original: OriginalSecurityAccount = {
    version: 1 as const, kind: 'hosted-account' as const, hostedOrigin: 'https://accounts.example.test',
    session, coordinator, state,
    openingId: 'opening_A_000000000000', taskId: 'task_A_000000000000000', requestId: 'request_A_000000000000',
    intent: {purpose: 'password_set_or_change' as const, target: '', operationId: 'operation_A_0000000000'},
    provider: state.providers[0],
    createdAt: iso(0), expiresAt: iso(120000),
  };
  const pending: AccountSecurityPending = {original};
  const successor: OriginalSecurityAccount = {...original, openingId: 'opening_B_000000000000', taskId: 'task_B_000000000000000', requestId: 'request_B_000000000000',
    intent: {...original.intent, operationId: 'operation_B_0000000000'}};
  const enriched: AccountSecurityPending = {original, transactionId: 'oid_' + 'T'.repeat(43)};
  return {session, state, coordinator, original, pending, successor, enriched, now: NOW};
}
type Mutable<T> = T extends object ? {-readonly [K in keyof T]: Mutable<T[K]>} : T;
export const clone = <T,>(value: T): Mutable<T> => JSON.parse(JSON.stringify(value)) as Mutable<T>;
