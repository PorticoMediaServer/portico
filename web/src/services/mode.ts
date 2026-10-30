/** Portico Account pages at web.getportico.tv. Inside the app they render in place (H14); a
 * cold load of one starts the Services entry only to capture a link it carries. */
export const servicesPaths = ['/account', '/device', '/reset', '/reset-password', '/verify-email', '/claim', '/join', '/sign-in', '/app/account-return'] as const;
const continuation = /^#(signIn=|join$|setup$|local$)/;

/** Which entry a web.getportico.tv document starts. Only a link that must be captured and
 * scrubbed before anything else runs (a reset or verification token, a claim, an invitation, a
 * device code, a sign-in or security return, a native return) starts the Services entry. Every
 * other address, the account pages included, is the app, which shows the account pages inside
 * the same document so moving between them never reloads the page. */
export async function hostedServicesPage(page: Pick<Location, 'pathname' | 'search' | 'hash'>): Promise<boolean> {
  const {pathname, search, hash} = page, query = new URLSearchParams(search);
  if (pathname === '/app/account-return') return true;
  if (pathname === '/') return continuation.test(hash) || query.has('mode') || query.has('identityError');
  if (hash.startsWith('#signIn=')) return true;
  if (pathname === '/account') return query.has('securityIntent') || query.has('identityTransaction') || query.has('identityError');
  if (pathname === '/sign-in') return false;
  return (servicesPaths as readonly string[]).includes(pathname) && Boolean(search || hash);
}
