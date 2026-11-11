import {createRoot} from 'react-dom/client';
import '../kit/tokens.css';
import '../kit/fonts.css';
import '../bootstrap.css';
import {ServicesApp} from './ServicesApp';
import {captureProviderReturn} from './AccountProviders';

if (import.meta.env.VITE_HOSTED_WEB !== 'true') throw new Error('Portico Account pages are served only at the Portico Account address.');
const nonce = document.querySelector<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content;
if (nonce && nonce !== '__PORTICO_STYLE_NONCE__') (window as Window & {__webpack_nonce__?: string}).__webpack_nonce__ = nonce;
// The one-use code after Google or Apple is read and scrubbed from the address before anything renders.
const arrival = location.pathname === '/app/account-return' ? captureProviderReturn() : undefined;
createRoot(document.getElementById('root')!).render(<ServicesApp arrival={arrival} />);
