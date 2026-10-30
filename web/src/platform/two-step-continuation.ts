/** An owner who turned on "Two-step verification" while switching the server to
 * local sign-in sets it up right after, in Profile & account. Only that choice
 * travels, bound to the one server and new local account it was made for; no
 * password is held, so the setup asks for it as it always does. In memory, one
 * at a time, taken once. */
type Target = Readonly<{serverId: string; accountId: string}>;
let pending: Target | undefined;
const matches = (serverId: string, accountId: string) => !!pending && pending.serverId === serverId && pending.accountId === accountId;
export function requestTwoStepSetup(target: Target): void {pending = Object.freeze({serverId: target.serverId, accountId: target.accountId});}
export function hasTwoStepSetup(serverId: string, accountId: string): boolean {return matches(serverId, accountId);}
export function takeTwoStepSetup(serverId: string, accountId: string): boolean {
  if (!matches(serverId, accountId)) return false;
  pending = undefined; return true;
}
export function discardTwoStepSetup(): void {pending = undefined;}
