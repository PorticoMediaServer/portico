import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {createSetupController, type SetupController} from '@core/setup-controller.ts';
import {readSetupPageReturn, type SetupPageReturn} from '@core/setup-portico.ts';
import {installationIdentity} from '@core/installation.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {defineWebScreen} from '../../route-registry';
import {router} from '../../router';
import {browserAccountOrigin} from '../../platform/account-origin';
import {createWebSetupAdapter, type WebSetupAdapter} from '../../platform/setup-adapter';
import {browserSetupStore} from '../../platform/setup-store';
import {browserAppSessions, browserConnectionEnvironment, restoreBrowserConnection} from '../../platform/server-connections';
import {browserSetupPairing} from './browser-setup';
import {SetupScreen} from './SetupScreen';

type SetupOpening = typeof router.state.location;
/** A return from Portico Services, between the page that arrived with it and the setup opened once it is out of the address. */
let carried: SetupPageReturn | undefined;
type SetupLease = Readonly<{opening: SetupOpening; adapter: WebSetupAdapter; controller: SetupController; key: number; current: () => boolean}>;

function SetupRoute() {
  const opening = useLocation({select: value => value});
  const [published, publish] = useState<SetupLease>(), generation = useRef(0);
  useLayoutEffect(() => {
    let live = true;
    const current = () => live && opening.pathname === '/setup' && router.state.location === opening;
    if (!current()) return;
    const origin = location.origin;
    // Back from Portico Services: what this address carries is taken out of the address bar and the history entry at once
    // (the approval is used once, by this page only). The setup that reads it takes it, whichever opening of this page
    // that is (cleaning the address can open the page again).
    const found = readSetupPageReturn(location.search, location.hash);
    if (found) {carried = found; history.replaceState(history.state, '', location.pathname);}
    const adapter = createWebSetupAdapter({pageReturn: () => {const value = carried; carried = undefined; return value;}, hostedOrigin: browserAccountOrigin(import.meta.env, location), pageOrigin: () => location.origin,
      pair: browserSetupPairing(() => origin), sessions: browserAppSessions, environment: browserConnectionEnvironment, restore: restoreBrowserConnection,
      remember: rememberNativeSession, store: browserSetupStore, installationId: () => installationIdentity()?.installationId,
      navigate: url => {if (current()) location.assign(url);}, current});
    const controller = createSetupController(adapter.ports, {foreground: document.visibilityState === 'visible'});
    const visibility = () => controller.setForeground(document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', visibility);
    // Setup done (library added or skipped): on to the shell.
    const stop = controller.subscribe(() => {if (controller.getSnapshot().kind === 'done' && current()) void router.navigate({to: '/', replace: true});});
    publish({opening, adapter, controller, key: ++generation.current, current});
    controller.start();
    return () => {live = false; stop(); document.removeEventListener('visibilitychange', visibility); controller.dispose(); adapter.dispose();};
  }, [opening]);
  const owned = published?.opening === opening && published.current() ? published : undefined;
  return owned ? <SetupScreen key={owned.key} controller={owned.controller} serverName={owned.adapter.serverName} signIn={owned.adapter.signIn} libraryFolders={owned.adapter.libraryFolders}/> : null;
}
export default defineWebScreen({id: 'setup', component: SetupRoute, chrome: 'standalone'});
