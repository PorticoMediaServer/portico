/** The Portico Account pages' own addresses at web.getportico.tv (the hosted mode of this app). */
export function isServicesPath(path: string): boolean {
  return ['/sign-in', '/sign-up', '/sign-up/confirm', '/reset', '/approve', '/account', '/app/account-return', '/tv', '/join'].includes(path) || /^\/approve\/[^/]+$/.test(path);
}
