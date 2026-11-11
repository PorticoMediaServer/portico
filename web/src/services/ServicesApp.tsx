import {useLayoutEffect, useMemo, useSyncExternalStore, type ReactNode} from 'react';
import {createRootRoute, createRoute, createRouter, Outlet, RouterProvider, useParams, type RouterHistory} from '@tanstack/react-router';
import {registerInPageNavigation, goTo} from '../platform/hosted-navigation';
import {IconSprite} from '../kit/Icon';
import {ServicesAuthStage} from '../kit/accounts/ServicesAuth';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {accountSession} from './account-session';
import {AccountSignIn, returnPath} from './AccountSignIn';
import {AccountConfirm, AccountSignUp} from './AccountSignUp';
import {AccountReset} from './AccountReset';
import {ApproveServer} from './ApproveServer';
import {AccountProviderReturn} from './AccountProviders';
import {AccountWorkspace} from './AccountWorkspace';
import {LinkTV} from './LinkTV';
import {isServicesPath} from './paths';

type Arrival = {id?: string; code?: string; failed: boolean};
const root = createRootRoute({component: () => <DocumentBootReady><Outlet /></DocumentBootReady>, notFoundComponent: () => null});
const next = () => returnPath();

function SignedOutOnly({children}: {children: ReactNode}) {
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  useLayoutEffect(() => { if (signedIn) goTo(next(), {replace: true}); }, [signedIn]);
  return signedIn ? <ServicesAuthStage><></></ServicesAuthStage> : children;
}
const routes = (arrival?: Arrival) => [
  createRoute({getParentRoute: () => root, path: '/sign-in', component: () => <SignedOutOnly><AccountSignIn next={next()} /></SignedOutOnly>}),
  createRoute({getParentRoute: () => root, path: '/sign-up', component: () => <SignedOutOnly><AccountSignUp next={next()} /></SignedOutOnly>}),
  createRoute({getParentRoute: () => root, path: '/sign-up/confirm', component: () => <AccountConfirm next={next()} />}),
  createRoute({getParentRoute: () => root, path: '/reset', component: () => <AccountReset next={next()} />}),
  createRoute({getParentRoute: () => root, path: '/approve', component: () => <ApproveServer />}),
  createRoute({getParentRoute: () => root, path: '/approve/$id', component: function ApproveById() { const {id} = useParams({strict: false}) as {id: string}; return <ApproveServer id={id} />; }}),
  createRoute({getParentRoute: () => root, path: '/account', component: () => <AccountWorkspace />}),
  createRoute({getParentRoute: () => root, path: '/tv', component: () => <LinkTV />}),
  createRoute({getParentRoute: () => root, path: '/app/account-return', component: () => <AccountProviderReturn arrival={arrival ?? {failed: true}} />}),
];

/** The Portico Account pages at web.getportico.tv (the hosted mode of this app). A cold load of
 * one of their addresses starts them alone (services/main.tsx); inside the app they render in
 * place on the app's own history (screens/accounts/HostedServicesHost.tsx). This router's types
 * stay local: the server app remains the only global TanStack Register. */
export function ServicesApp({history, arrival}: {history?: RouterHistory; arrival?: Arrival}) {
  const router = useMemo(() => createRouter({...(history ? {history} : {}), routeTree: root.addChildren(routes(arrival))}), [history, arrival]);
  // Opened alone, the account pages change pages in place; the app's own pages are a page load
  // away. Inside the app, the app's router owns this.
  useLayoutEffect(() => history ? undefined : registerInPageNavigation((to, replace) => {
    if (isServicesPath(new URL(to, location.origin).pathname)) { if (replace) router.history.replace(to); else router.history.push(to); }
    else if (replace) location.replace(to); else location.assign(to);
  }), [history, router]);
  return <><IconSprite /><RouterProvider router={router} /></>;
}
