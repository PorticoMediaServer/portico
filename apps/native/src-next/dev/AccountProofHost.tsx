import React, {useEffect, useState} from 'react';
import {defaultI18n} from '../../../../packages/i18n/src';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {beginNativeInstallation} from '../platform/installation';
import {HomeSkeleton, StateView} from '../kit/states';
import {AccountEntry} from '../screens/accounts/AccountEntry';
import {RestoredShell} from '../shell/RestoredShell';

/** Reachable only through App's existing DEV KitHost. This is the genuine
 * installation -> credential flow -> durable session -> restoration assembly,
 * without a synthetic viewer/runtime or an alternate storage/HTTP adapter. */
export function AccountProofHost({serverAddress}: {serverAddress: string}) {
  const [attempt, setAttempt] = useState(0), [state, setState] = useState<Readonly<{ready?: boolean; error?: unknown}>>({});
  const [complete, setComplete] = useState(false);
  useEffect(() => {
    const abort = new AbortController(), installation = beginNativeInstallation(abort.signal);
    setState({}); setComplete(false);
    void installation.prepare().then(() => {if (!abort.signal.aborted) setState({ready: true});}, error => {
      if (!abort.signal.aborted) setState({error});
    });
    return () => {abort.abort(); installation.dispose();};
  }, [serverAddress, attempt]);
  if (state.error) {
    const error = presentError(state.error, 'sign-in');
    return <StateView variant="centered" title={error.title} body={error.body} tone="error"
      primaryAction={{id: 'retry-installation', label: defaultI18n.t('action.tryAgain'), onPress: () => setAttempt(value => value + 1)}}/>;
  }
  if (!state.ready) return <HomeSkeleton visible elapsedMs={0}/>;
  return complete ? <RestoredShell/> : <AccountEntry initialStage="address" initialAddress={serverAddress} onComplete={() => setComplete(true)}/>;
}
