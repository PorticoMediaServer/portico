import {createContext, useContext, useEffect, type ReactNode} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateView} from '../kit/states';
import {ServerStarting} from '../kit/states/ServerStarting';
import {LaunchUnreachableScreen} from './LaunchUnreachable';
import {router} from '../router';
import {rememberRequestedPath} from '../platform/requested-path';
import {currentWebEnvironment} from '../platform/environment';
import {currentServedServerOrigin} from '../platform/served-origin';
import {BootProgress} from './Boot';
import {DocumentBootReady} from './DocumentBootReady';
import type {ShellBootstrapSnapshot} from './bootstrap';

type BootstrapState = Readonly<{snapshot: ShellBootstrapSnapshot; retry: () => Promise<void>; retryNow: () => Promise<void>}>;
const Context = createContext<BootstrapState | undefined>(undefined);
export function ShellBootstrapState({value, children}: {value: BootstrapState | undefined; children: ReactNode}) { return <Context.Provider value={value}>{children}<BootProgress snapshot={value?.snapshot}/></Context.Provider>; }
export const useShellBootstrapState = () => useContext(Context);
export function BootstrapContent({children}: {children: ReactNode}) {
  const state = useContext(Context);
  const path = useRouterState({select: state => state.location.pathname});
  const needsSignIn = state?.snapshot.phase === 'signed-out' && !state.snapshot.entry
    // A link to a server this account can't open says so (O-161) with no server selected; it needs none.
    && !['/setup', '/sign-in', '/profiles', '/servers', '/account', '/link-unavailable'].includes(path) && !path.startsWith('/dev/');
  const needsProfile = state?.snapshot.phase === 'choose-profile' && path !== '/profiles';
  const focus = state?.snapshot.focus;
  useEffect(() => {
    // The page asked for opens once a profile does (sign-in and Who's watching come first).
    if (needsSignIn || needsProfile) rememberRequestedPath(router.state.location.href);
    // Sign-in is for a server: this page's own, or web.getportico.tv's Portico Account. A page no
    // server served (the desktop app) has neither and starts at the server chooser.
    if (needsSignIn) void router.navigate({to: currentWebEnvironment().hostedWeb || currentServedServerOrigin() ? '/sign-in' : '/servers', replace: true});
    else if (needsProfile) void router.navigate({to: '/profiles', replace: true, ...(focus ? {search: {profileId: focus}} : {})});
  }, [needsSignIn, needsProfile, focus]);
  if (needsSignIn || needsProfile) return null;
  if (state?.snapshot.phase === 'restoring') return <DocumentBootReady ready={!!state.snapshot.waiting}/>;
  let content = children;
  if (state?.snapshot.phase === 'unreachable' && state.snapshot.unreachable?.category === 'starting') content = <ServerStarting onSwitchServer={() => {void router.navigate({to: '/servers'});}}/>;
  else if (state?.snapshot.phase === 'unreachable' && state.snapshot.unreachable) content = <LaunchUnreachableScreen facts={state.snapshot.unreachable}
    onRetry={() => { void state.retryNow(); }} onSwitchServer={() => { void router.navigate({to: '/servers'}); }} />;
  else if (state?.snapshot.phase === 'error') content = <StateView title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: defaultI18n.t('next.states.tryAgain'), onPress: () => { void state.retry(); }}} />;
  // Every destination owns its outline and loading clock. In particular a title,
  // library or reader deep link never mounts a Home skeleton before its own page.
  return <DocumentBootReady>{content}</DocumentBootReady>;
}
