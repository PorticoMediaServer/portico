import type {PorticoServerAccess} from './portico-server-access';

/**
 * A page that can't reach web.getportico.tv (the desktop app's window) signs in with a Portico
 * Account the way a TV does: a code approved in the person's browser. The platform installs it
 * before the app starts; it answers the account it signed in, held for this sign-in only.
 */
export type PorticoCodeSignIn = (signal: AbortSignal) => Promise<PorticoServerAccess>;

let installed: PorticoCodeSignIn | undefined;
export function installPorticoCodeSignIn(signIn: PorticoCodeSignIn): () => void {
  installed = signIn;
  return () => { if (installed === signIn) installed = undefined; };
}
export const porticoCodeSignIn = (): PorticoCodeSignIn | undefined => installed;
