import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Linking, NativeModules} from 'react-native';
import {readPorticoAccountSecurity} from '../../../../../../packages/client-core/src/portico-services-native';
import {providerOnlySignIn} from '../../../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {useTheme} from '../../../kit/theme';
import {nativeAccounts} from '../../../platform/accounts';
import {hostedUrl} from '../../../platform/account-authority';
import {NativeAccountSecurity, type NativeAccountSecurityKind} from '../../../platform/account-security';
import {accountExpectation, nativeAccountRead} from '../../../platform/account-work';
import {nativeNetworkFetch} from '../../../platform/server-connections';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {accountSecurityAvailability} from '../accounts/useAccountSecurity';
import {createNativeAccountSecurityScope, nativeAccountSecurityAllowed} from '../../../screens/settings-live/native-account-security';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
type Opening = Readonly<{current: () => boolean; refresh: () => Promise<void>}>;
const noSubscription = () => () => {};

/** Settings › Password or Two-step for a Portico Account: samples the matching Portico session once per original server
 * Settings opening, and owns every action and guard. Moved from NativeHostedSignInPanel as it was; the panel only draws. */
export function useHostedSignIn({kind, context, lifetime, runtime}: Pick<NativeSettingsPanelDeps, 'context' | 'lifetime'> & {kind: 'password' | 'twoStep'; runtime: NativeShellRuntime}) {
  const theme = useTheme(), navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const retirement = runtime.retirementSignal;
  const [opening, setOpening] = useState<Opening>(), openingRef = useRef<Opening | undefined>(undefined);
  // A Google- or Apple-only account (Justin, 1 Nov): Add a password waits behind More sign-in options.
  const [moreSignIn, setMoreSignIn] = useState(false);
  // Add a password and Two-step's "Set a password" open the hosted reset page after one plain line (#3392).
  const [resetAsk, setResetAsk] = useState(false);
  const [source, setSource] = useState<NativeAccountSecurity>(), [failure, setFailure] = useState<string>();
  useLayoutEffect(() => {
    setOpening(undefined); setSource(undefined); setFailure(undefined);
    if (theme.isTV || retirement?.aborted || !lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind)) return;
    const hostedAccountId = context.account.account.hostedAccountId;
    // A local account never even consults the Portico credential owner.
    if (!hostedAccountId) return;
    const credentials = nativeAccounts.accountCredentials(), original = credentials.getSnapshot().session;
    if (!original || original.account.id !== hostedAccountId) return;
    let settings: ReturnType<typeof createNativeAccountSecurityScope>;
    try {settings = createNativeAccountSecurityScope(context, lifetime, kind);}
    catch (error) {setFailure(presentError(error, 'portico-account').body); return;}
    const scope = {accountId: original.account.id, familyId: original.familyId};
    let active = true, loading = false, controller: NativeAccountSecurity | undefined;
    const cleanups: (() => void)[] = [];
    const current = () => {
      const session = credentials.getSnapshot().session;
      return active && openingRef.current === held && settings.current() && !retirement?.aborted
        && context.account.account.hostedAccountId === scope.accountId
        && session?.account.id === scope.accountId && session.familyId === scope.familyId;
    };
    const dispose = (notify = false) => {
      if (!active) return;
      active = false; controller?.dispose(); controller = undefined;
      for (const cleanup of cleanups.splice(0)) cleanup();
      settings.dispose();
      if (openingRef.current === held) {
        openingRef.current = undefined;
        if (notify) {setOpening(undefined); setSource(undefined); setFailure(undefined);}
      }
    };
    const changed = () => {if (!current()) dispose(true);};
    const refresh = async () => {
      if (!current() || loading) return;
      loading = true; controller?.dispose(); controller = undefined; setSource(undefined); setFailure(undefined);
      const request = new AbortController(), abort = () => request.abort();
      settings.signal.addEventListener('abort', abort, {once: true});
      const timer = setTimeout(abort, 15000);
      try {
        const session = credentials.getSnapshot().session!;
        const state = await nativeAccountRead(credentials, value => readPorticoAccountSecurity(
          value.accessToken, hostedUrl, scope, nativeNetworkFetch, request.signal),
        {expected: accountExpectation(session), signal: request.signal, isCurrent: current});
        if (!current()) return;
        controller = new NativeAccountSecurity(nativeAccounts, scope, state, current); setSource(controller);
      } catch (error) {if (current()) setFailure(presentError(error, 'portico-account').body);}
      finally {loading = false; clearTimeout(timer); settings.signal.removeEventListener('abort', abort);}
    };
    const held: Opening = {current, refresh}; openingRef.current = held; setOpening(held);
    const stop = credentials.subscribe(changed); cleanups.push(stop);
    settings.signal.addEventListener('abort', changed, {once: true});
    cleanups.push(() => settings.signal.removeEventListener('abort', changed));
    retirement?.addEventListener('abort', changed, {once: true});
    cleanups.push(() => retirement?.removeEventListener('abort', changed));
    changed(); void refresh();
    return () => dispose();
  }, [context, lifetime, kind, navigation.revision, navigation.route, retirement, theme.isTV]);
  // A local account never consults the Portico credential owner, here either.
  const linked = context.account.account.hostedAccountId, credentials = linked ? nativeAccounts.accountCredentials() : undefined;
  const signedInAccount = useSyncExternalStore(credentials?.subscribe ?? noSubscription, () => credentials?.getSnapshot().session?.account.id);
  const shown = !(theme.isTV || !lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind));
  // H09 on native: the app holds no Portico Account session for this server's account: say who the server knows and open
  // the Portico Account's own Security page in the browser.
  const elsewhere = !!linked && signedInAccount !== linked;
  const manage = () => {if (lifetime.current() && context.current()) void Linking.openURL(`${hostedUrl}/account?tab=security`).catch(error => {if (lifetime.current()) setFailure(presentError(error, 'portico-account').body);});};
  const ready = opening?.current() ? source : undefined, security = ready?.security;
  const locked = security ? accountSecurityAvailability(security, NativeModules.PorticoAccountAuth).credentialLocked : true;
  const askReset = () => {if (opening?.current() && ready?.current()) setResetAsk(true);};
  const open = (task: NativeAccountSecurityKind) => {if (!locked && opening?.current() && ready?.current()) ready.open(task);};
  const refreshNow = () => {if (opening?.current()) void opening.refresh();};
  const providerOnly = kind === 'password' && security ? providerOnlySignIn(security) : undefined;
  return {shown, elsewhere, username: context.account.account.username, manage, failure, ready, security, locked, askReset, open, refreshNow,
    canRetry: !!opening?.current(), retry: () => {if (opening) void opening.refresh();}, providerOnly, moreSignIn, showMoreSignIn: () => setMoreSignIn(true),
    resetAsk, closeReset: () => setResetAsk(false)};
}
