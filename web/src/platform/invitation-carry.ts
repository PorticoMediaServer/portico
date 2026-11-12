/** An invitation on its way to Join at web.getportico.tv (`/servers#join`): its code and the
 * server's HTTPS address. It is kept in this tab only while the account signs in (a sign-in loses
 * the address's fragment), then read once. Never in a query, which a server would receive. */
const carried = 'portico.join.handoff';
export type CarriedInvitation = Readonly<{code: string; address: string}>;
/** A sign-in begun for an invitation and then left: after this, the next person in the tab does not
 * meet it filled in. */
const LASTS_MS = 10 * 60_000;
export function carryInvitation(code: string, address: string, now = Date.now()): void {
  try {sessionStorage.setItem(carried, JSON.stringify({code, address: /^https:\/\/[^/?#]+$/.test(address) ? address : '', at: now}));} catch {/* private window: typed again */}
}
/** The account's sign-in in this browser ended: an invitation carried for it goes too. */
export function forgetCarriedInvitation(): void {
  try {sessionStorage.removeItem(carried);} catch {/* nothing kept */}
}
export function takeCarriedInvitation(now = Date.now()): CarriedInvitation | undefined {
  try {
    const raw = sessionStorage.getItem(carried); sessionStorage.removeItem(carried);
    const value = raw ? JSON.parse(raw) as {code?: unknown; address?: unknown; at?: unknown} : undefined;
    if (!value || typeof value.at !== 'number' || now - value.at > LASTS_MS || now < value.at) return undefined;
    return typeof value.code === 'string' && typeof value.address === 'string' ? Object.freeze({code: value.code, address: value.address}) : undefined;
  } catch {return undefined;}
}
