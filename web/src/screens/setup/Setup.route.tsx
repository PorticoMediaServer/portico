import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defineWebScreen} from '../../route-registry';
import {router} from '../../router';
import {browserAccountOrigin} from '../../platform/account-origin';
import {browserSetupDraft} from '../../platform/setup-claim';
import {BrowserSetupComposition} from '../../platform/setup-composition';
import {createBrowserSetupPorts} from './browser-setup';
import {SetupScreen} from './SetupScreen';
import type {SetupPorts} from './setup-flow';

type SetupOpening = typeof router.state.location;
type SetupLease = Readonly<{opening: SetupOpening; ports: SetupPorts; key: number; current: () => boolean}>;

function SetupRoute() {
  const opening = useLocation({select: value => value});
  const [published, publish] = useState<SetupLease>(), generation = useRef(0);
  useLayoutEffect(() => {
    let live = true;
    const current = () => live && opening.pathname === '/setup' && router.state.location === opening;
    if (!current()) return;
    const origin = location.origin;
    const lifetime = new BrowserSetupComposition(browserAccountOrigin(import.meta.env, location), undefined, undefined, current);
    const base = createBrowserSetupPorts({origin: () => origin, draft: browserSetupDraft, acceptLocal: lifetime.acceptLocal,
      continueHosted: lifetime.continueHosted, resumeHosted: lifetime.resumeHosted, addLibrary: lifetime.addLibrary, libraryFolders: lifetime.libraryFolders,
      complete: () => {if (current()) void router.navigate({to: '/', replace: true});}});
    const ports: SetupPorts = {...base, connect: async signal => {
      if (!current()) throw new DOMException('Setup opening changed', 'AbortError');
      const target = await lifetime.connect(base.connect, signal);
      if (!current()) {target.dispose();throw new DOMException('Setup opening changed', 'AbortError');}
      return target;
    }};
    publish({opening, ports, key: ++generation.current, current});
    return () => {live = false;lifetime.dispose();};
  }, [opening]);
  const owned = published?.opening === opening && published.current() ? published : undefined;
  return owned ? <SetupScreen key={owned.key} ports={owned.ports} /> : null;
}
export default defineWebScreen({id: 'setup', component: SetupRoute, chrome: 'standalone'});
