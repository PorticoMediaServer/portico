/** An invitation on its way to Join at web.getportico.tv (`/servers#join`): its code and the
 * server's HTTPS address. It is kept in this tab only while the account signs in (a sign-in loses
 * the address's fragment), then read once. Never in a query, which a server would receive. */
const carried = 'portico.join.handoff';
export type CarriedInvitation = Readonly<{code: string; address: string}>;
export function carryInvitation(code: string, address: string): void {
  try {sessionStorage.setItem(carried, JSON.stringify({code, address: /^https:\/\/[^/?#]+$/.test(address) ? address : ''}));} catch {/* private window: typed again */}
}
export function takeCarriedInvitation(): CarriedInvitation | undefined {
  try {
    const raw = sessionStorage.getItem(carried); sessionStorage.removeItem(carried);
    const value = raw ? JSON.parse(raw) as {code?: unknown; address?: unknown} : undefined;
    return value && typeof value.code === 'string' && typeof value.address === 'string' ? Object.freeze({code: value.code, address: value.address}) : undefined;
  } catch {return undefined;}
}
