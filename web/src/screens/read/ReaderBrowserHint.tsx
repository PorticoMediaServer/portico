import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../../kit/Icon';
import styles from './browser-hint.module.css';

type InstallOffer = Event & {prompt: () => Promise<void>; userChoice: Promise<{outcome: 'accepted' | 'dismissed'}>};
const key = 'portico.reader.browser-hint.dismissed.v1';
let dismissedForVisit = false;
function standalone() {
  return window.matchMedia('(display-mode: standalone)').matches || (navigator as Navigator & {standalone?: boolean}).standalone === true;
}
function safariHomeScreen() {
  // Safari supplies this real installation flag on iOS; other iOS browsers
  // do not offer Safari's Share -> Add to Home Screen instructions.
  return (navigator as Navigator & {standalone?: boolean}).standalone === false
    && /Safari\//.test(navigator.userAgent) && !/CriOS|FxiOS|EdgiOS|OPiOS/.test(navigator.userAgent);
}
/** phone-read 12: browser installation guidance only, never offline-reading authority. */
export function ReaderBrowserHint({visible}: Readonly<{visible: boolean}>) {
  const [kind, setKind] = useState<'safari' | 'install'>(), [busy, setBusy] = useState(false);
  const offer = useRef<InstallOffer | undefined>(undefined);
  useEffect(() => {
    let dismissed = dismissedForVisit;
    try {dismissed ||= localStorage.getItem(key) === '1';} catch { /* Private/disabled storage keeps the visit-only choice. */ }
    if (dismissed || standalone()) return;
    if (safariHomeScreen()) setKind('safari');
    const available = (event: Event) => {
      if (!event.isTrusted || standalone() || typeof (event as Partial<InstallOffer>).prompt !== 'function') return;
      event.preventDefault(); offer.current = event as InstallOffer; setKind('install');
    };
    const installed = () => {offer.current = undefined; setKind(undefined);};
    const display = window.matchMedia('(display-mode: standalone)');
    const changed = () => {if (standalone()) installed();};
    window.addEventListener('beforeinstallprompt', available); window.addEventListener('appinstalled', installed); display.addEventListener('change', changed);
    return () => {window.removeEventListener('beforeinstallprompt', available); window.removeEventListener('appinstalled', installed); display.removeEventListener('change', changed); offer.current = undefined;};
  }, []);
  const dismiss = () => {
    dismissedForVisit = true; offer.current = undefined; setKind(undefined);
    try {localStorage.setItem(key, '1');} catch { /* No book, viewer or credential data is persisted. */ }
  };
  if (!kind || !visible) return null;
  const {t} = defaultI18n;
  return <aside className={styles.hint} aria-label={t('next.reader.readFullScreen')}>
    <Icon name="i-book" size={24}/><span><b>{t('next.reader.readFullScreen')}</b><br/>{t(kind === 'safari' ? 'next.reader.addHomeScreen' : 'next.reader.installForFullScreen')}</span>
    {kind === 'install' && <button type="button" disabled={busy} onClick={() => {
      const captured = offer.current; if (!captured || busy || standalone()) return;
      // Consume only the actual one-shot browser event, in this user gesture.
      offer.current = undefined; setBusy(true);
      void (async () => {await captured.prompt(); await captured.userChoice;})().then(dismiss, () => {setKind(undefined);}).finally(() => setBusy(false));
    }}>{t('next.reader.installApp')}</button>}
    <button type="button" disabled={busy} onClick={dismiss}>{t('next.reader.notNow')}</button>
  </aside>;
}
