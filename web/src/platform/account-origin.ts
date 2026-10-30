/** Packaged server web uses the public account service; local Hosted is opt-in or development only. */
export function browserAccountOrigin(env: {VITE_HOSTED_URL?: string; VITE_ACCOUNT_WORKSPACE?: string; DEV?: boolean}, page: {origin: string; hostname: string}): string {
  if (env.VITE_HOSTED_URL) return env.VITE_HOSTED_URL;
  if (env.VITE_ACCOUNT_WORKSPACE === 'true' || page.hostname === 'web.getportico.tv') return page.origin;
  return env.DEV ? 'http://127.0.0.1:19410' : 'https://web.getportico.tv';
}
