import './platform/polyfills';
import {createRoot} from 'react-dom/client';
import './kit/tokens.css';
import './kit/fonts.css';
import './bootstrap.css';
import {initializeAppInstallation} from './platform/installation-provider';
import {captureWebAccountArrival} from './route-registry-arrival';
import {browserSecurityJournal, retireOrdinarySecurityOpening} from './platform/account-security-journal';
import {AccountSecurityReturnEntry, type AccountSecurityEntry} from './services/AccountSecurityReturnEntry';
import {AppLifetime} from './AppLifetime';
import {installSettingsPlatform, settingsPlatform} from './platform/settings-platform';
import {webSettingsPlatform} from './platform/web-settings-platform';
import {IconSprite} from './kit/Icon';
import {LastResortBoundary, StartupFailure} from './shell/Errors';
import {DocumentBootReady} from './shell/DocumentBootReady';

const accountArrival = captureWebAccountArrival(location, history);
const securityHint = Boolean(accountArrival.takeAccountSecurity);
let capturedReturn = accountArrival.takeAccountSecurity?.() ?? accountArrival.takeAccountReturn?.();
let securityEntry: AccountSecurityEntry | undefined;
const takeOrdinaryReturn = accountArrival.takeAccountReturn ? () => {const value=capturedReturn;capturedReturn=undefined;return value;} : undefined;
/** A native host installs its own device port before it imports this module; the browser is the fallback. */
const retireSettingsPlatform = settingsPlatform() ? () => {} : installSettingsPlatform(webSettingsPlatform());
const styleNonce = document.querySelector<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content;
if (styleNonce && styleNonce !== '__PORTICO_STYLE_NONCE__') {
  (window as Window & {__webpack_nonce__?: string}).__webpack_nonce__ = styleNonce;
}
const lifetime = new AppLifetime();
/** Desktop closes the real app before removing native brokers/platform ports.
 * This retires mounted owners, never signs out or clears protected credentials. */
export function disposeApp(): void {
  try { lifetime.dispose(); } finally { try { retireSettingsPlatform(); } finally { capturedReturn=undefined;securityEntry=undefined;accountArrival.dispose(); } }
}
// Core services own real effects; test their lifecycle directly instead of double mounting them.
async function start() {
  if (capturedReturn) {
    let protectedSecurity = false;
    try {protectedSecurity = Boolean(await browserSecurityJournal().read(true));} catch {protectedSecurity = securityHint || globalThis.isSecureContext === true && Boolean(navigator.locks);}
    if (lifetime.signal.aborted) return;
    if (securityHint || protectedSecurity) {securityEntry = Object.freeze({kind: 'server', captured: capturedReturn});capturedReturn=undefined;}
  }
  let failure: {error: unknown} | undefined;
  let normal: {App: typeof import('./App')['App']; Bootstrap: typeof import('./shell/bootstrap')['ShellBootstrap']; assemble: typeof import('./shell/assembly')['assembleShellBindings']} | undefined;
  return lifetime.start(async signal => {
    if (securityEntry) return;
    await retireOrdinarySecurityOpening().catch(() => undefined);
    signal.throwIfAborted();
    try { await initializeAppInstallation(import.meta.env.VITE_APP_VERSION ?? '', signal); }
    catch (error) { signal.throwIfAborted(); failure = {error}; }
    if (!failure) {
      try {
        const [app, bootstrap, assembly] = await Promise.all([import('./App'), import('./shell/bootstrap'), import('./shell/assembly')]);
        signal.throwIfAborted();normal = {App: app.App, Bootstrap: bootstrap.ShellBootstrap, assemble: assembly.assembleShellBindings};
      } catch (error) {signal.throwIfAborted();failure = {error};}
    }
  }, () => {
    const bootstrap = !normal || securityEntry || failure || (import.meta.env.DEV && location.pathname.startsWith('/dev/')) ? undefined : new normal.Bootstrap(normal.assemble);
    const AppComponent = normal?.App;
    let root: ReturnType<typeof createRoot> | undefined;
    const unmount = () => {try { bootstrap?.dispose(); } finally { root?.unmount(); root = undefined; }};
    try {
      root = createRoot(document.getElementById('root')!);
      root.render(<DocumentBootReady ready={!securityEntry && Boolean(failure || !AppComponent)}>{securityEntry ? <><IconSprite/><LastResortBoundary><AccountSecurityReturnEntry arrival={securityEntry}/></LastResortBoundary></> : failure
        ? <><IconSprite /><LastResortBoundary><StartupFailure error={failure.error} /></LastResortBoundary></>
        : AppComponent ? <AppComponent bootstrap={bootstrap} takeAccountReturn={takeOrdinaryReturn} takeAccountMove={accountArrival.takeAccountMove} /> : <><IconSprite/><StartupFailure error={new Error('The app did not initialize.')}/></>}</DocumentBootReady>);
      return unmount;
    } catch (error) { unmount(); throw error; }
  });
}
if (!(window as Window & {__porticoUnsupportedBrowser?: boolean}).__porticoUnsupportedBrowser) void start();
