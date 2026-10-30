import React, {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Linking, NativeModules, Text, View} from 'react-native';
import {HttpLocalApi} from '../../../../../packages/client-core/src';
import {parseSecurityState} from '../../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {routePath} from '../../../../../packages/contracts/src/hosted/routes';
import {Button} from '../../kit/controls';
import {SettingsRow} from '../../kit/shell';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {nativeAccounts} from '../../platform/accounts';
import {hostedUrl} from '../../platform/account-authority';
import {NativeAccountSecurity, type NativeAccountSecurityKind} from '../../platform/account-security';
import {accountExpectation, nativeAccountRead} from '../../platform/account-work';
import {nativeNetworkFetch} from '../../platform/server-connections';
import type {NativeShellRuntime} from '../../shell/runtime';
import {AccountSecuritySheet, accountSecurityAvailability} from '../accounts/PhoneAccountSecurity';
import {createNativeAccountSecurityScope, nativeAccountSecurityAllowed} from './native-account-security';
import type {NativeSettingsPanelDeps} from './settings-panels';

type Opening = Readonly<{current: () => boolean; refresh: () => Promise<void>}>;
const noSubscription = () => () => {};

/** Sample the matching Portico session once per original server Settings opening. */
export function NativeHostedSignInPanel({kind, context, lifetime, t, runtime}: NativeSettingsPanelDeps & {
  kind: 'password' | 'twoStep'; runtime: NativeShellRuntime;
}) {
  const theme = useTheme(), navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const retirement = runtime.retirementSignal;
  const [opening, setOpening] = useState<Opening>(), openingRef = useRef<Opening | undefined>(undefined);
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
        const state = await nativeAccountRead(credentials, async value => parseSecurityState(
          await new HttpLocalApi(hostedUrl, value.accessToken, nativeNetworkFetch).request(routePath('account_security_get'), 'GET', undefined, request.signal), scope),
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
  if (theme.isTV || !lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind)) return null;
  // H09 on native: the app holds no Portico Account session for this server's account (signed out of
  // it here, or another account). Never draw dead rows: say who the server knows and open the Portico
  // Account's own Security page in the browser. One row, under Password; Two-step adds nothing.
  if (linked && signedInAccount !== linked) return kind !== 'password' ? null : <>
    <SettingsRow first testID="settings-panel-portico-account"
      label={t('settings.row.porticoAccount')} value={context.account.account.username}
      control={<Button testID="settings-manage-portico-account" variant="glass" size="compact"
        onPress={() => {if (lifetime.current() && context.current()) void Linking.openURL(`${hostedUrl}/account?tab=security`).catch(error => {if (lifetime.current()) setFailure(presentError(error, 'portico-account').body);});}}>{t('web.signIn.manage')}</Button>}/>
    {failure && <View style={{padding: 14}}><Text accessibilityRole="alert" style={[shellType(14, 400, 20), {color: theme.colors.danger}]}>{failure}</Text></View>}
  </>;
  const ready = opening?.current() ? source : undefined, security = ready?.security;
  const locked = security ? accountSecurityAvailability(security, NativeModules.PorticoAccountAuth).credentialLocked : true;
  const action = (task: NativeAccountSecurityKind, label: string) => ready && <Button testID={`account-security-${task}`} variant="glass" size="compact" disabled={locked}
    onPress={() => {if (!locked && opening?.current() && ready.current()) ready.open(task);}}>{label}</Button>;
  return <>
    <SettingsRow first testID={`settings-panel-${kind}`} label={t(kind === 'password' ? 'settings.row.password' : 'settings.row.twoStep')}
      value={kind === 'twoStep' && security ? t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off') : undefined}
      control={security ? kind === 'password' ? action('password', t(security.hasPassword ? 'web.hostedSecurity.change' : 'account.setPassword'))
        : <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><Text style={[shellType(14.5), {color: theme.colors.textSecondary}]}>{t(security.mfaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off')}</Text>
          {action(security.mfaEnabled ? 'mfa-off' : 'mfa-on', t(security.mfaEnabled ? 'web.hostedSecurity.turnOff' : 'web.twoStep.turnOn'))}</View> : undefined}/>
    {kind === 'twoStep' && security?.mfaEnabled && <SettingsRow testID="settings-panel-recovery-codes" label={t('web.hostedSecurity.recoveryCodes')}
      control={action('codes', t('web.hostedSecurity.makeCodes'))}/>}
    {failure && <View style={{padding: 14, gap: 12}}><Text accessibilityRole="alert" style={[shellType(14, 400, 20), {color: theme.colors.danger}]}>{failure}</Text>
      {opening?.current() && <Button variant="glass" size="compact" onPress={() => {void opening.refresh();}}>{t('action.tryAgain')}</Button>}</View>}
    {security && <AccountSecuritySheet security={security} source={ready} onFinished={() => {if (opening?.current()) void opening.refresh();}}/>}
  </>;
}
