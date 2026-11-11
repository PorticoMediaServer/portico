import {isServicesPath} from './paths';

/** Which entry a web.getportico.tv document starts. The Portico Account pages start alone, without
 * the app, so signing up, signing in or approving a server loads only what those pages need. Every
 * other address is the app, which shows the same account pages in place when it moves to one. */
export async function hostedServicesPage(page: Pick<Location, 'pathname'>): Promise<boolean> {
  return isServicesPath(page.pathname);
}
