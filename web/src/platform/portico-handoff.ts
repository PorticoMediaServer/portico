/** Where a Portico Accounts server's own page sends a sign-in: web.getportico.tv opens the server
 * from the account's list (`/servers?open=<id>`). Joining carries the invitation's code, and this
 * page's address when it is HTTPS, in the fragment, which no server receives. A plain-HTTP address
 * is left out: web.getportico.tv can't reach it. */
export function porticoHandoffUrl(input: Readonly<{hostedOrigin: string; serverId: string; pageOrigin: string; invitation?: string}>): Readonly<{url: string; secure: boolean}> {
  const url = new URL('/servers', input.hostedOrigin), secure = new URL(input.pageOrigin).protocol === 'https:';
  url.searchParams.set('open', input.serverId);
  if (input.invitation) {
    const fragment = new URLSearchParams({invitation: input.invitation});
    if (secure) fragment.set('address', new URL(input.pageOrigin).origin);
    url.hash = fragment.toString();
  }
  return Object.freeze({url: url.href, secure});
}
