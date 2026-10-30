import {useShellFacts} from '../../shell/fact-hooks';
import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {localDeviceHint} from '../../platform/local-device-entry';
import {AuthStage} from '../../kit/accounts/AuthStage';
import {StateView} from '../../kit/states';
import {LinkTvDialog} from '../settings/LinkTvDialog';
import {router} from '../../router';
import {captureLocalDeviceOpening, type LocalDeviceLocation, type LocalDeviceOpening} from './local-device-opening';

type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type Owner = {context: Captured['context']; scope: string; location: LocalDeviceLocation; proof: LocalDeviceOpening; live: boolean; code?: string};
const t = defaultI18n.t;

function DeviceEntry() {
  const captured = useProtectedWebScreenContext(), location = useLocation();
  if (!captured) return null; // The normal bootstrap owns sign-in and restoration.
  return <LocalDevicePage key={JSON.stringify([captured.binding.scopeKey, location.href, location.state.__TSR_key])} captured={captured} location={location}/>;
}
function LocalDevicePage({captured, location}: {captured: Captured; location: LocalDeviceLocation}) {
  useShellFacts(captured.context);
  const {context, binding} = captured, scope = binding.scopeKey;
  const original = useRef<Owner | undefined>(undefined), focus = useRef<HTMLHeadingElement>(null);
  const seed = useRef<Readonly<{context: Captured['context']; code?: string}> | undefined>(undefined);
  const [displayed, display] = useState<Owner | undefined>(undefined), [failed, fail] = useState(false);
  useLayoutEffect(() => {
    const proof = captureLocalDeviceOpening({pageOrigin: window.location.origin, api: context.api, session: context.session,
      isCurrent: context.isCurrent, location, getLocation: () => router.state.location});
    if (!proof) {fail(true);return;}
    // Consume only after the actual same-server principal/pin/entry is proved.
    // Replay gets a fresh owner, retaining this entry's immutable display hint.
    if (!seed.current) seed.current = {context, code: localDeviceHint.take(window.location.origin)};
    const held: Owner = {context, scope, location, proof, live: true, code: seed.current.context === context ? seed.current.code : undefined};
    original.current = held;fail(false);display(held);
    const retire = () => {
      if (!held.live) return;
      held.live = false;
      if (original.current === held) original.current = undefined;
      proof.dispose();
    };
    const check = () => {
      if (!held.live || original.current !== held) return;
      if (!proof.current()) {retire();display(value => value === held ? undefined : value);fail(true);}
    };
    const offRoute = context.api.subscribeRoute(check), offAuth = context.auth.subscribe(check);
    const offNavigation = router.subscribe('onBeforeNavigate', event => {
      if (event.toLocation !== location) retire();
    });
    return () => {retire();offNavigation();offAuth();offRoute();};
  }, [context, scope, location]);
  const held = displayed?.context === context && displayed.scope === scope && displayed.location === location && displayed.live ? displayed : undefined;
  const close = () => {
    if (!held || original.current !== held || !held.proof.current()) return;
    held.live = false;original.current = undefined;held.proof.dispose();display(undefined);
    void router.navigate({to: '/'}).catch(error => {if (context.isCurrent()) binding.home.effects.reportError(error);});
  };
  if (!held) return failed ? <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'home', label: t('next.states.goHome'), onPress: () => {
      if (context.isCurrent() && router.state.location === location) void router.navigate({to: '/'}).catch(binding.home.effects.reportError);
    }}}/> : null;
  return <AuthStage>
    <h1 ref={focus} tabIndex={-1}>{t('web.device.title')}</h1>
    <p>{context.facts.serverName}</p><p>{t('web.device.approvingAs', {name: context.facts.identity.username})}</p>
    <LinkTvDialog open api={held.proof.api} initialCode={held.code} returnFocusRef={focus} onOpenChange={open => {if (!open) close();}}/>
  </AuthStage>;
}
export default defineWebScreen({id: 'device', chrome: 'standalone', component: DeviceEntry});
