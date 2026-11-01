import {createRoot} from 'react-dom/client';
import '../kit/tokens.css';
import '../kit/fonts.css';
import '../bootstrap.css';
import {IconSprite} from '../kit/Icon';
import {browserAccountOrigin} from '../platform/account-origin';
import {initializeAppInstallation} from '../platform/installation-provider';
import {ServicesApp, ServicesInvalidLink} from './ServicesApp';
import {captureServicesEntry} from './entry';
import {AccountSecurityReturnEntry} from './AccountSecurityReturnEntry';
import {retireOrdinarySecurityOpening} from '../platform/account-security-journal';
import {captureHostedSecurityArrival} from './account-security-return';
import {DocumentBootReady} from '../shell/DocumentBootReady';

// Scrub a link synchronously, before React, storage or the account singleton is started.
const securityReturn = captureHostedSecurityArrival(location, history);
const entry: ReturnType<typeof captureServicesEntry> = securityReturn ? {capture: undefined, invalidContinuation: false} : captureServicesEntry(location, history, browserAccountOrigin(import.meta.env, location));
const nonce = document.querySelector<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content;
if (nonce && nonce !== '__PORTICO_STYLE_NONCE__') (window as Window & {__webpack_nonce__?: string}).__webpack_nonce__ = nonce;
if (import.meta.env.VITE_HOSTED_WEB !== 'true') throw new Error('Portico Account pages are served only at the Portico Account address.');
if (entry.native !== 'forwarded' && !(window as Window & {__porticoUnsupportedBrowser?: boolean}).__porticoUnsupportedBrowser) {
  // Reuse the MAIN browser-floor shims only in a protected context with real cross-tab locks.
  // Its LAN fallbacks must never substitute for Services credential/continuation protection.
  if (entry.native === 'invalid') createRoot(document.getElementById('root')!).render(<DocumentBootReady><IconSprite /><ServicesInvalidLink /></DocumentBootReady>);
  else {
    if (globalThis.isSecureContext && globalThis.crypto?.subtle && navigator.locks) await import('../platform/polyfills');
    // Device metadata only: a failed claim leaves it unset and never blocks sign-in.
    if (!securityReturn) await retireOrdinarySecurityOpening().catch(() => undefined);
    if (!securityReturn) await initializeAppInstallation(import.meta.env.VITE_APP_VERSION ?? '').catch(() => undefined);
    createRoot(document.getElementById('root')!).render(securityReturn ? <><IconSprite/><AccountSecurityReturnEntry arrival={securityReturn}/></> : <ServicesApp capture={entry.capture} invalidContinuation={entry.invalidContinuation} join={entry.join} plainShortJoin={entry.plainShortJoin} signIn={entry.signIn} owner={entry.owner} providerFailure={entry.providerFailure} />);
  }
}
