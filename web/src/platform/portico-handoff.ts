/** Where a Portico Accounts server's own page sends a sign-in: web.getportico.tv opens the server
 * from the account's list (`/servers?open=<id>`). Joining carries the invitation's code, and a
 * waiting member's move the owner's move code, in the fragment, which no server receives, with this
 * page's address when it is HTTPS. A plain-HTTP address is left out: web.getportico.tv can't reach it. */
export function porticoHandoffUrl(input: Readonly<{hostedOrigin: string; serverId: string; pageOrigin: string; invitation?: string; move?: Readonly<{code: string} | {ticket: string}>}>): Readonly<{url: string; secure: boolean}> {
  const url = new URL('/servers', input.hostedOrigin), secure = new URL(input.pageOrigin).protocol === 'https:';
  url.searchParams.set('open', input.serverId);
  // A move's proof: the owner's code (`move=`), or the server's ticket for an old password (`ticket=`).
  const carried: Record<string, string> | undefined = input.invitation ? {invitation: input.invitation}
    : input.move ? 'code' in input.move ? {move: input.move.code} : {ticket: input.move.ticket} : undefined;
  if (carried) {
    const fragment = new URLSearchParams(carried);
    if (secure) fragment.set('address', new URL(input.pageOrigin).origin);
    url.hash = fragment.toString();
  }
  return Object.freeze({url: url.href, secure});
}
