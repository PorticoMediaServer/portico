import {currentWebEnvironment} from './environment';

type Page = Readonly<{protocol: string; hostname: string; origin: string}>;

/** The Portico server that served this page, when one did: its own address (demo.getportico.tv, a
 * LAN address, a development server). web.getportico.tv and the desktop app's own pages are not
 * served by a server, so they have none and keep the server chooser. */
export function servedServerOrigin(page: Page, environment: Readonly<{hostedWeb: boolean; serverUrl?: string}>): string | undefined {
  if (environment.hostedWeb) return undefined;
  if (environment.serverUrl) return environment.serverUrl;
  if (page.protocol !== 'http:' && page.protocol !== 'https:' || page.hostname === 'tauri.localhost') return undefined;
  return page.origin;
}
export const currentServedServerOrigin = () => servedServerOrigin(location, {hostedWeb: currentWebEnvironment().hostedWeb, serverUrl: import.meta.env.VITE_SERVER_URL});

/** After Forget on the saved-server identity page: a page a server served signs in to that server
 * at once; only a page no server served goes to the chooser. */
export function forgetDestination(served: string | undefined): Readonly<{to: 'sign-in'; origin: string}> | Readonly<{to: 'servers'}> {
  return served ? {to: 'sign-in', origin: served} : {to: 'servers'};
}

/** server-identity.html frame 6: a saved server whose own page this is shows no identity page,
 * because the page came from the server at this address and a check here proves nothing. Its
 * stale saved sign-in is dropped and sign-in opens for the server now here. */
export function savedServerIsThisPage(savedOrigin: string, served: string | undefined): boolean {
  if (!served) return false;
  try { return new URL(savedOrigin).origin === new URL(served).origin; } catch { return false; }
}
