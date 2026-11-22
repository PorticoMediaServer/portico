import {useEffect, useState} from 'react';
import {Linking} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {nativePorticoRecovery, type NativePorticoRecovery, type NativeProfileRecovery} from '../../../platform/profile-recovery';
import {hostedUrl} from '../../../platform/account-authority';

type MessageKey = Parameters<typeof defaultI18n.t>[0];

/** Auth18 on a phone (Forgot PIN). The controller owns every credential and the temporary proof family; this hook keeps
 * only what the person types, hands each value over once (clearing it), and works out which step the page shows.
 * Moved from the old screen as it was. */
export function useProfileRecovery(controller: NativeProfileRecovery, preparePortico: () => Promise<NativePorticoRecovery> = nativePorticoRecovery) {
  const [state, setState] = useState(controller.getSnapshot());
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [pin, setPin] = useState(''), [twoStep, setTwoStep] = useState('');
  // On a Portico Accounts server the password is the Portico Account's, typed here and checked by Portico Services.
  // The provider step: which provider was launched (its refusal words differ), the two-step code it asks
  // for after the provider returns, and a password Services reported meanwhile.
  const [launched, setLaunched] = useState<'google' | 'apple'>(), [codeAsk, setCodeAsk] = useState<Readonly<{resolve: (code: string) => void; reject: (error: unknown) => void}>>();
  const [passwordAppeared, setPasswordAppeared] = useState(false);
  useEffect(() => {if ((state.error as {code?: unknown} | undefined)?.code === 'account_password_exists') setPasswordAppeared(true);}, [state.error]);
  const [portico, setPortico] = useState<Readonly<{phase: 'loading'} | {phase: 'ready'; value: NativePorticoRecovery} | {phase: 'failed'; error: unknown}>>(), [attempt, setAttempt] = useState(0);
  const porticoStep = state.phase === 'portico';
  useEffect(() => {
    if (!porticoStep) return;
    let live = true; setPortico({phase: 'loading'});
    preparePortico().then(value => {if (live) setPortico({phase: 'ready', value});}, error => {if (live) setPortico({phase: 'failed', error});});
    return () => {live = false;};
  }, [porticoStep, attempt, preparePortico]);
  useEffect(() => {
    const off = controller.subscribe(() => setState(controller.getSnapshot()));
    return () => {off(); controller.dispose();};
  }, [controller]);
  const refused = (state.error as {code?: unknown} | undefined)?.code;
  const ready = portico?.phase === 'ready' ? portico.value : undefined;
  const account = ready?.account;
  // A password added while a provider page was away: Services says so, and the password form takes over.
  const providers = account?.providers ?? [], hasPassword = !!account?.hasPassword || refused === 'account_password_exists' || passwordAppeared;
  const refusedForPassword = refused === 'account_password_required';
  const providerSentence: MessageKey | undefined = refused === 'provider_sign_in_not_recent' ? (launched === 'apple' ? 'accounts.profiles.porticoAppleNotRecent' : 'accounts.profiles.porticoGoogleNotRecent')
    : refused === 'provider_unavailable_here' ? 'accounts.profiles.porticoProviderUnavailable'
    // The system sheet reports a cancel and a failure alike (`provider_cancelled`): either way nothing changed.
    : refused === 'provider_return_incomplete' || refused === 'provider_cancelled' ? 'accounts.profiles.porticoProviderIncomplete' : undefined;
  return {state, refused, error: state.error ? presentError(state.error, 'profiles').body : undefined,
    porticoLoading: !portico || portico.phase === 'loading', porticoFailure: portico?.phase === 'failed' ? presentError(portico.error, 'profiles').body : undefined,
    retryPortico: () => setAttempt(value => value + 1), account,
    providerStep: !!account && !hasPassword && !refusedForPassword && providers.length > 0,
    passwordNeeded: !!account && (!hasPassword || refusedForPassword), providerSentence,
    providers: [...providers].sort((a, b) => a === 'apple' ? -1 : b === 'apple' ? 1 : 0),
    password, setPassword, code, setCode, pin, setPin, twoStep, setTwoStep, codeAsk: !!codeAsk,
    porticoReady: !!password && (!account?.twoStep || twoStep.trim().length === 6),
    setPinNow: (value: string) => {void controller.reset(value);},
    sendCode: () => {const value = code; setCode(''); void controller.code(value);},
    sendPassword: () => {const value = password; setPassword(''); void controller.password(value);},
    sendPorticoPassword: () => {if (!ready) return; const value = password, typed = twoStep.trim(); setPassword(''); setTwoStep(''); void controller.porticoPassword(value, typed, ready.source);},
    startProvider: (provider: 'google' | 'apple') => {
      if (!ready) return;
      setLaunched(provider);
      const asked = ready.account.twoStep ? () => new Promise<string>((resolve, reject) => setCodeAsk({resolve, reject})) : '';
      void controller.porticoProvider(provider, asked, ready.source);
    },
    sendProviderCode: () => {if (!codeAsk) return; const typed = twoStep.trim(); setTwoStep(''); setCodeAsk(undefined); codeAsk.resolve(typed);},
    cancelProviderCode: (onClose: () => void) => {if (!codeAsk) return; setCodeAsk(undefined); codeAsk.reject(Object.assign(new Error('cancelled'), {name: 'AbortError'})); onClose();},
    openSetPassword: () => {void Linking.openURL(hostedUrl + '/account?tab=security');}};
}
