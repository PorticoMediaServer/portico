import {createContext, useContext, useEffect, type ReactNode} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateView} from '../kit/states';
import {ServerStarting} from '../kit/states/ServerStarting';
import {LaunchUnreachableScreen} from './LaunchUnreachable';
import {router} from '../router';
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
    && !['/setup', '/sign-in', '/profiles', '/servers', '/account'].includes(path) && !path.startsWith('/dev/');
  useEffect(() => {
    if (needsSignIn) void router.navigate({to: '/sign-in', replace: true});
  }, [needsSignIn]);
  if (needsSignIn) return null;
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
