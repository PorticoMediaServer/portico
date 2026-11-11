import {useLayoutEffect} from 'react';
import {useRouter} from '@tanstack/react-router';
import {ServicesApp} from '../../services/ServicesApp';
import {isServicesPath} from '../../services/paths';
import {registerInPageNavigation} from '../../platform/hosted-navigation';

/** The Portico Account pages at web.getportico.tv, inside the app (H14): moving between them and
 * the app's own pages stays in this one document, on the app's history. */
export const isHostedServicesPath = isServicesPath;

export function HostedServicesHost({active}: {active: boolean}) {
  const router = useRouter();
  return active ? <div data-hosted-services><ServicesApp history={router.history} /></div> : null;
}

/** Same-origin moves anywhere in the app use its router's history: never a page load. */
export function useHostedInPageNavigation(enabled: boolean) {
  const router = useRouter();
  useLayoutEffect(() => enabled ? registerInPageNavigation((to, replace) => {if (replace) router.history.replace(to); else router.history.push(to);}) : undefined, [enabled, router]);
}
