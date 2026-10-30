import {useEffect, useRef, useState} from 'react';
import type {AccountIdentityFailureBinding} from '../../platform/account-identity-failure';
import {AuthError, AuthStage, ServerIdentityFailure} from '../../kit/accounts';

/** Renderer only: every action remains bound to the controller's captured saved entry. */
export function AccountIdentityFailure({binding}: {binding: AccountIdentityFailureBinding}) {
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const lifetime = useRef({active: true});
  useEffect(() => {const epoch = {active: true}; lifetime.current = epoch; return () => {epoch.active = false;};}, []);
  const retry = async () => {
    if (busy || !binding.isCurrent()) return;
    const epoch = lifetime.current; setBusy(true); setError(undefined);
    try {await binding.onRetry();}
    catch (failure) {if (epoch.active && binding.isCurrent()) setError(failure);}
    finally {if (epoch.active) setBusy(false);}
  };
  if (!binding.isCurrent()) return null;
  return <AuthStage legal={false}><ServerIdentityFailure serverName={binding.serverName} failure={binding.failure} busy={busy}
    onRetry={() => {void retry();}} onChooseServer={binding.onChooseServer} onForget={binding.onForget}/><AuthError error={error}/></AuthStage>;
}
