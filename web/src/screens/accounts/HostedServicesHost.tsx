import {useEffect, useLayoutEffect, useState} from 'react';
import {useRouter} from '@tanstack/react-router';
import {ServicesApp} from '../../services/ServicesApp';
import {registerInPageNavigation} from '../../platform/hosted-navigation';

/** The Portico Account pages at web.getportico.tv, inside the app (H14). Opening them, changing
 * tabs, Join, Set up and the server chooser all stay in this one document: no page load, no boot
 * screen. They share the app's history, and stay mounted (hidden) once warmed, so returning to
 * them draws at once instead of restoring again. */
export const hostedServicesPaths: ReadonlySet<string> = new Set(['/account', '/device', '/join', '/reset', '/reset-password', '/verify-email', '/claim', '/sign-in', '/welcome']);

export function HostedServicesHost({active}: {active: boolean}) {
  const router = useRouter();
  const [warm, setWarm] = useState(active);
  useEffect(() => {
    if (active) {setWarm(true); return;}
    // After the app's own first page, so this never competes with it or its boot screen.
    const timer = setTimeout(() => setWarm(true), 1500);
    return () => clearTimeout(timer);
  }, [active]);
  return warm ? <div hidden={!active} data-hosted-services><ServicesApp history={router.history} /></div> : null;
}

/** Same-origin moves anywhere in the app use its router's history: never a page load. */
export function useHostedInPageNavigation(enabled: boolean) {
  const router = useRouter();
  useLayoutEffect(() => enabled ? registerInPageNavigation((to, replace) => {if (replace) router.history.replace(to); else router.history.push(to);}) : undefined, [enabled, router]);
}
