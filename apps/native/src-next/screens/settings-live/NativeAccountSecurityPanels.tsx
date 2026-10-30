import React, {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {apiScopes, createAccessToken, getApiAccessOptions, listAccessTokens, revokeAccessToken, type ApiAccessOptions, type ApiAccessTransport, type ApiCredential, type ApiCredentialPage, type ApiScope, type IssuedApiCredential} from '../../../../../packages/client-core/src/api-access';
import {IdentityClient, type TwoFactorEnrolment} from '../../../../../packages/client-core/src/identity-client';
import type {Device} from '../../../../../packages/client-core/src/identity-devices';
import type {TwoFactorState} from '../../../../../packages/client-core/src/identity-restrictions';
import {passwordStrength} from '../../../../../packages/client-core/src/password-strength';
import {idempotencyKey} from '../../../../../packages/client-core/src/playback-v1/http';
import {EXPIRY_DAYS, chosenScopes, credentialWorks, expiryInstant, expiryLabel, type ExpiryDays} from '../../../../../packages/client-core/src/server-admin/api-words';
import {createOperationIds} from '../../../../../packages/client-core/src/server-admin/server-forms';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {nativeAccounts} from '../../platform/accounts';
import {copyText} from '../../platform/clipboard';
import {Button, Select, Switch, TextField} from '../../kit/controls';
import {BottomSheet, Dialog} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSettingsPanelDeps} from './settings-panels';
import {NativeHostedSignInPanel} from './NativeHostedSignInPanel';
import {createNativeAccessTokenScope, createNativeAccountSecurityScope, nativeAccessTokensAllowed, nativeAccountSecurityAllowed, type NativeAccessTokenScope, type NativeAccountSecurityKind, type NativeAccountSecurityScope} from './native-account-security';

type Opening = Readonly<{scope: NativeAccountSecurityScope; client: IdentityClient}>;
type Mode = 'main' | 'disable' | 'confirm-disable' | 'replace' | 'confirm-remove' | 'confirm-all';

/** Local account credentials and this server's devices. Hosted credential changes retain their account-owned flow. */
export function NativeAccountSecurityPanel({kind, context, lifetime, t, runtime, openRoute}: NativeSettingsPanelDeps & {kind: NativeAccountSecurityKind; runtime: NativeShellRuntime}) {
  const theme = useTheme(), dimensions = useWindowDimensions(), navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const [opening, setOpening] = useState<Opening>(), openingRef = useRef<Opening | undefined>(undefined);
  const mounted = useRef(false), busyRef = useRef(false), copyRef = useRef(false), shownCodes = useRef<readonly string[] | undefined>(undefined);
  const stageRef = useRef<readonly unknown[]>([]);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [notice, setNotice] = useState('');
  const [factor, setFactor] = useState<TwoFactorState>(), [enrolment, setEnrolment] = useState<TwoFactorEnrolment>(), [codes, setCodes] = useState<readonly string[]>();
  const [password, setPassword] = useState(''), [nextPassword, setNextPassword] = useState(''), [confirmation, setConfirmation] = useState(''), [code, setCode] = useState('');
  const [devices, setDevices] = useState<readonly Device[]>(), [device, setDevice] = useState<Device>(), [deviceName, setDeviceName] = useState('');
  const [mode, setMode] = useState<Mode>('main'), [copied, setCopied] = useState(false);
  const clear = () => {
    shownCodes.current = undefined;
    setError(undefined); setFactor(undefined); setEnrolment(undefined); setCodes(undefined); setPassword(''); setNextPassword(''); setConfirmation(''); setCode('');
    setDevices(undefined); setDevice(undefined); setDeviceName(''); setMode('main'); setCopied(false); setBusy(false);
  };
  const close = () => {openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; setOpening(undefined); clear();};
  useLayoutEffect(() => {
    mounted.current = true; clear(); setNotice(''); setOpening(undefined);
    return () => {mounted.current = false; openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; shownCodes.current = undefined;};
  }, [context, lifetime, kind, navigation.revision, navigation.route]); // Clear plaintext on a different Settings opening.
  const current = (held: Opening | undefined): held is Opening => !!held && mounted.current && openingRef.current === held && !runtime.retirementSignal?.aborted && held.scope.current()
    && (kind === 'devices' || !context.account.account.hostedAccountId);
  const run = async <T,>(held: Opening | undefined, work: (client: IdentityClient, scope: NativeAccountSecurityScope) => Promise<T>, accepted: (value: T) => void) => {
    if (!current(held) || busyRef.current) return;
    busyRef.current = true; setBusy(true); setError(undefined);
    try {const value = await work(held.client, held.scope); if (current(held)) accepted(value);}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (current(held)) {busyRef.current = false; setBusy(false);}}
  };
  const load = (held: Opening) => {
    if (kind === 'devices') void run(held, (client, source) => client.devices(source.installationId ?? '', source.signal), setDevices);
    else void run(held, (client, source) => client.twoFactor(source.signal), setFactor);
  };
  const open = () => {
    if (kind !== 'devices' && context.account.account.hostedAccountId) return;
    if (openingRef.current || !mounted.current || !lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind)) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== navigation.revision || now.route !== navigation.route || now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    close(); setNotice('');
    let scope: NativeAccountSecurityScope;
    try {scope = createNativeAccountSecurityScope(context, lifetime, kind);} catch (failure) {if ((failure as {name?: string})?.name === 'AbortError') return; throw failure;}
    const held = {scope, client: new IdentityClient(scope)};
    openingRef.current = held; setOpening(held); load(held);
  };
  const held = opening;
  const stage = [mode, factor, enrolment, codes, device, password, nextPassword, confirmation, code, deviceName] as const;
  stageRef.current = stage;
  const stageCurrent = () => current(held) && stage.every((value, index) => stageRef.current[index] === value);
  const perform = <T,>(work: (client: IdentityClient, scope: NativeAccountSecurityScope) => Promise<T>, accepted: (value: T) => void) => {if (stageCurrent()) return run(held, work, accepted);};
  const revealCodes = (value: readonly string[]) => {shownCodes.current = value; setCodes(value);};
  const edit = (setter: (value: string) => void) => (value: string) => {if (stageCurrent() && !busyRef.current) setter(value);};
  const go = (next: Mode) => {if (stageCurrent() && !busyRef.current) {setMode(next); setPassword(''); setCode(''); setError(undefined);}};
  const saved = (message: string) => {close(); setNotice(message);};
  const signOutHere = async (scope: NativeAccountSecurityScope) => {
    if (!scope.current()) return;
    await nativeAccounts.signOutServer(scope.serverId, scope.familyId);
  };
  const refreshDevices = async (client: IdentityClient, scope: NativeAccountSecurityScope) => client.devices(scope.installationId ?? '', scope.signal);
  const copyCodes = async () => {
    if (!stageCurrent() || !codes?.length || copyRef.current) return;
    copyRef.current = true;
    try {await copyText(codes.join('\n')); if (current(held)) {setCopied(true); setError(undefined);}}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (openingRef.current === held) copyRef.current = false;}
  };
  if (!lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind) || theme.isTV && kind !== 'devices') return null;
  if (kind !== 'devices' && context.account.account.hostedAccountId) return <NativeHostedSignInPanel kind={kind} context={context} lifetime={lifetime} t={t} runtime={runtime} openRoute={openRoute}/>;
  const label = t(kind === 'password' ? 'settings.row.password' : kind === 'twoStep' ? 'settings.row.twoStep' : 'settings.section.devices');
  const strength = passwordStrength(nextPassword), matching = !confirmation || confirmation === nextPassword;
  const bodyText = (value: string) => <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{value}</Text>;
  const passwordField = <TextField testID="account-security-current-password" label={kind === 'password' ? t('account.currentPassword') : t('auth.password')} value={password} onChangeText={edit(setPassword)} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="current-password" textContentType="password" editable={!busy}/>;
  const codeField = <TextField testID="account-security-code" label={t('auth.verificationCode')} description={enrolment ? undefined : t('web.twoStep.orRecoveryCode')} value={code} onChangeText={edit(setCode)} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode" editable={!busy}/>;
  let title = label, description: string | undefined, body: React.ReactNode = null, actions: React.ReactNode = null;
  if (codes) {
    title = t('confirm.recoveryCodes.title'); description = t('confirm.recoveryCodes.body');
    body = <Text testID="account-security-recovery-codes" selectable style={[shellType(17, 700, 28), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{codes.join('\n')}</Text>;
    actions = <><Button testID="account-security-copy-codes" variant="glass" onPress={() => void copyCodes()}>{t('web.twoStep.copy')}</Button><Button testID="account-security-codes-saved" onPress={() => {if (current(held)) close();}}>{t('confirm.recoveryCodes.action')}</Button></>;
  } else if (kind === 'password' && factor) {
    title = t('web.reset.submit');
    body = <>{passwordField}
      <TextField testID="account-security-new-password" label={t('accounts.settings.changeOwnerLocalPassword')} value={nextPassword} onChangeText={edit(setNextPassword)} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword" editable={!busy}/>
      {nextPassword && <View accessibilityLabel={strength.label} style={{gap: 5}}><View style={{height: 4, flexDirection: 'row', gap: 3}}>{[1, 2, 3, 4].map(level => <View key={level} style={{flex: 1, borderRadius: 2, backgroundColor: level <= strength.level ? theme.colors.accentStrong : theme.colors.line}}/>)}</View>{bodyText([strength.label, strength.hint].filter(Boolean).join(' · '))}</View>}
      <TextField testID="account-security-confirm-password" label={t('accounts.signIn.confirmPassword')} value={confirmation} onChangeText={edit(setConfirmation)} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword" editable={!busy} error={!matching ? t('account.passwordMismatch') : undefined}/>
      {factor.enabled && codeField}</>;
    actions = <Button testID="account-security-save-password" loading={busy} disabled={busy || !password || !strength.acceptable || !confirmation || !matching || factor.enabled && !code.trim()} onPress={() => {
      if (!password || !strength.acceptable || confirmation !== nextPassword || factor.enabled && !code.trim()) return;
      void perform(client => client.changePassword(password, nextPassword, factor.enabled ? code.trim() : undefined), () => saved(t('web.reset.doneTitle')));
    }}>{t('action.save')}</Button>;
  } else if (kind === 'twoStep' && factor) {
    if (mode === 'confirm-disable') {
      title = t('web.twoStep.turnOffConfirmTitle'); description = t('security.twoStep.turnOffBody');
      actions = <Button testID="account-security-confirm-disable" variant="danger" loading={busy} disabled={busy} onPress={() => void perform(client => client.disableTwoFactor(password, code.trim()), () => saved('Two-step verification turned off.'))}>{t('web.hostedSecurity.turnOff')}</Button>;
    } else if (mode === 'disable' || mode === 'replace') {
      title = mode === 'disable' ? t('web.twoStep.turnOffTitle') : t('web.twoStep.replaceCodes');
      description = mode === 'replace' ? t('web.twoStep.replaceCodesBody') : t('web.twoStep.confirmPasswordShort');
      body = <>{passwordField}{codeField}</>;
      actions = <Button testID="account-security-factor-submit" loading={busy} disabled={busy || !password || !code.trim()} onPress={() => {
        if (!stageCurrent() || busyRef.current || !password || !code.trim()) return;
        if (mode === 'disable') {setMode('confirm-disable'); return;}
        void perform((client, scope) => client.replaceRecoveryCodes(password, code.trim(), scope.signal), value => {revealCodes(value); setPassword(''); setCode(''); setMode('main');});
      }}>{mode === 'disable' ? t('action.continue') : t('web.twoStep.replaceCodesAction')}</Button>;
    } else if (enrolment) {
      title = t('web.twoStep.scanTitle'); description = t('web.twoStep.scanBody');
      body = <><View style={{alignSelf: 'center', padding: 12, borderRadius: 14, backgroundColor: theme.colors.white}}><QRCode value={enrolment.uri} size={180}/></View>{bodyText(t('web.twoStep.cantScan'))}
        <Text selectable style={[shellType(16, 700, 24), {color: theme.colors.text}]}>{enrolment.secret.match(/.{1,4}/g)?.join(' ')}</Text>{codeField}</>;
      actions = <Button testID="account-security-verify" loading={busy} disabled={busy || !/^\d{6}$/.test(code.replace(/\s/g, ''))} onPress={() => {
        const verificationCode = code.replace(/\s/g, ''); if (!/^\d{6}$/.test(verificationCode)) return;
        void perform(async client => {const value = await client.verifyTwoFactor(password, verificationCode); if (!value.enabled) throw new Error('invalid_two_factor_state'); return value;}, value => {
          setFactor(value); revealCodes(enrolment.recoveryCodes); setEnrolment(undefined); setPassword(''); setCode('');
        });
      }}>{t('security.twoStep.verify')}</Button>;
    } else if (!factor.enabled) {
      title = t('web.twoStep.turnOnTitle'); description = t('security.twoStep.confirmPassword'); body = passwordField;
      actions = <Button testID="account-security-enrol" loading={busy} disabled={busy || !password} onPress={() => {if (password) void perform(client => client.enrolTwoFactor(password), value => {setEnrolment(value); setCode('');});}}>{t('action.continue')}</Button>;
    } else {
      body = <>{bodyText(t('web.hostedSecurity.on'))}{bodyText(t('web.hostedSecurity.codesLeft', {count: factor.recoveryCodesRemaining}))}</>;
      actions = <><Button testID="account-security-disable" variant="glass" onPress={() => go('disable')}>{t('web.hostedSecurity.turnOff')}</Button><Button testID="account-security-replace-codes" variant="glass" onPress={() => go('replace')}>{t('web.twoStep.replaceCodes')}</Button></>;
    }
  } else if (kind === 'devices' && devices) {
    if (mode === 'confirm-all') {
      title = t('account.signOutEverywhere.title'); description = t('web.devices.everywhereBody');
      actions = <Button testID="account-security-confirm-all" variant="danger" loading={busy} disabled={busy} onPress={() => void perform(async (client, scope) => {await client.signOutEverywhere(); await signOutHere(scope);}, () => close())}>{t('account.signOutEverywhere.action')}</Button>;
    } else if (device) {
      title = mode === 'confirm-remove' ? t('confirm.removeDevice.title', {device: device.name}) : device.name;
      description = mode === 'confirm-remove' ? t('confirm.removeDevice.body') : t('web.devices.firstSeen', {date: defaultI18n.date(device.firstSeen)});
      if (mode === 'confirm-remove') actions = <Button testID="account-security-confirm-remove" variant="danger" loading={busy} disabled={busy} onPress={() => {
        if (device.current) return;
        void perform(async (client, scope) => {await client.removeDevice(device.id); return refreshDevices(client, scope);}, value => {setDevices(value); setDevice(undefined); setMode('main');});
      }}>{t('web.devices.removeAction')}</Button>;
      else {
        body = <TextField testID="account-security-device-name" label={t('profile.name')} value={deviceName} onChangeText={edit(setDeviceName)} maxLength={120} editable={!busy}/>;
        actions = <><Button testID="account-security-sign-out-device" variant="glass" disabled={busy} onPress={() => void perform(async (client, scope) => {
          await client.signOutDevice(device.id); if (device.current) {await signOutHere(scope); return undefined;} return refreshDevices(client, scope);
        }, value => {if (value) {setDevices(value); setDevice(undefined);} else close();})}>{device.current ? t('device.signOutThis') : t('device.signOutOtherAction')}</Button>
          {!device.current && <Button testID="account-security-remove-device" variant="danger" disabled={busy} onPress={() => go('confirm-remove')}>{t('web.devices.removeAction')}</Button>}
          <Button testID="account-security-save-device" loading={busy} disabled={busy || !deviceName.trim() || deviceName.trim() === device.name} onPress={() => {
            if (!deviceName.trim() || deviceName.trim() === device.name) return;
            void perform(async (client, scope) => {await client.editDevice(device.id, {name: deviceName.trim()}); return refreshDevices(client, scope);}, value => {setDevices(value); setDevice(undefined);});
          }}>{t('web.devices.saveName')}</Button></>;
      }
    } else {
      body = <View>{devices.length ? devices.map((row, index) => <SettingsRow key={row.id} first={index === 0} testID={`account-security-device-${row.id}`} label={row.name}
        value={row.current ? t('settings.thisDevice') : undefined} description={[row.platform, row.app, defaultI18n.date(row.lastSeen), row.sessions ? t('web.devices.sessions', {count: row.sessions}) : 'Signed out'].filter(Boolean).join(' · ')}
        onPress={() => {if (stageCurrent() && !busyRef.current) {setDevice(row); setDeviceName(row.name); setMode('main'); setError(undefined);}}}/>) : bodyText(t('web.devices.noneSignedIn'))}</View>;
      actions = <Button testID="account-security-sign-out-all" variant="glass" disabled={busy} onPress={() => go('confirm-all')}>{t('next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.label')}</Button>;
    }
  }
  const cancel = () => {
    if (!stageCurrent() || shownCodes.current) return;
    if (mode === 'confirm-disable') {setMode('disable'); return;}
    if (mode === 'confirm-remove') {setMode('main'); return;}
    if (device) {setDevice(undefined); setMode('main'); return;}
    if (mode !== 'main') {go('main'); return;}
    close();
  };
  const surface = {
    testID: `account-security-${kind}`, open: !!opening && current(opening), title, description,
    onOpenChange: (value: boolean) => {if (!value && !shownCodes.current && current(held)) close();},
    dismissOnBack: !codes, dismissOnOutsidePress: !codes, dismissOnGesture: !codes, showClose: !codes,
    footer: <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{!codes && <Button variant="glass" disabled={busy} onPress={cancel}>{t(device ? 'action.back' : 'action.cancel')}</Button>}{actions}</View>,
  };
  const content = <View style={{gap: 12}}>
    {error !== undefined && <StateView testID="account-security-error" {...runtime.error(error, () => {if (current(held)) setError(undefined);}, 'account')}/>}
    {busy && !factor && !devices && bodyText(t('status.loading'))}
    {!busy && error !== undefined && !factor && !devices && <Button onPress={() => {if (current(held)) load(held);}}>{t('action.tryAgain')}</Button>}
    {body}{copied && bodyText(t('api.credential.copied'))}
  </View>;
  return <>
    <SettingsRow first testID={`settings-panel-${kind}`} label={label} onPress={open}/>
    {notice && <Text accessibilityLiveRegion="polite" style={[shellType(13, 400, 19), {color: theme.colors.textSecondary, padding: 14}]}>{notice}</Text>}
    {theme.platform === 'phone' ? <BottomSheet {...surface} fitContent keyboardAvoiding>{content}</BottomSheet>
      : <Dialog {...surface}><ScrollView keyboardShouldPersistTaps="handled" style={{maxHeight: Math.max(180, dimensions.height * .55)}}>{content}</ScrollView></Dialog>}
  </>;
}

type TokenOpening = Readonly<{scope: NativeAccessTokenScope; transport: ApiAccessTransport}>;
type TokenView = 'list' | 'create' | 'revoke' | 'receipt';

/** A member's own personal access tokens: list, create and revoke. The token is shown once,
 * in a sheet only its explicit acknowledgement closes, and nothing keeps it afterwards. */
export function NativeAccessTokensPanel({context, lifetime, t, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const theme = useTheme(), dimensions = useWindowDimensions(), navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const [opening, setOpening] = useState<TokenOpening>(), openingRef = useRef<TokenOpening>(undefined);
  const mounted = useRef(false), busyRef = useRef(false), copyRef = useRef(false);
  const operations = useRef(createOperationIds(() => idempotencyKey()));
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [notice, setNotice] = useState('');
  const [page, setPage] = useState<ApiCredentialPage>(), [options, setOptions] = useState<ApiAccessOptions>();
  const [view, setView] = useState<TokenView>('list'), [revoking, setRevoking] = useState<ApiCredential>();
  const [name, setName] = useState(''), [ticked, setTicked] = useState<ReadonlySet<ApiScope>>(() => new Set()), [days, setDays] = useState<ExpiryDays>(0);
  const [issued, setIssued] = useState<IssuedApiCredential>(), [copied, setCopied] = useState(false);
  const clear = () => {
    setBusy(false); setError(undefined); setPage(undefined); setOptions(undefined); setView('list'); setRevoking(undefined);
    setName(''); setTicked(new Set()); setDays(0); setIssued(undefined); setCopied(false);
  };
  const close = () => {openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; operations.current.release(); setOpening(undefined); clear();};
  useLayoutEffect(() => {
    mounted.current = true; clear(); setNotice(''); setOpening(undefined);
    return () => {mounted.current = false; openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false;};
  }, [context, lifetime, navigation.revision, navigation.route]); // A different Settings opening keeps no list or secret.
  const current = (held: TokenOpening | undefined): held is TokenOpening => !!held && mounted.current && openingRef.current === held && !runtime.retirementSignal?.aborted && held.scope.current();
  const run = async <T,>(held: TokenOpening | undefined, work: (held: TokenOpening) => Promise<T>, accepted: (value: T) => void) => {
    if (!current(held) || busyRef.current) return;
    busyRef.current = true; setBusy(true); setError(undefined);
    try {const value = await work(held); if (current(held)) accepted(value);}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (current(held)) {busyRef.current = false; setBusy(false);}}
  };
  const load = (held: TokenOpening | undefined, cursor = '', append = false) => {
    void run(held, h => Promise.all([listAccessTokens(h.transport, cursor, h.scope.signal), getApiAccessOptions(h.transport, h.scope.signal)]), ([next, access]) => {
      setOptions(access); setPage(value => append && value ? {...next, items: [...value.items, ...next.items]} : next);
    });
  };
  const open = () => {
    if (openingRef.current || !mounted.current || !lifetime.current() || !context.current() || !nativeAccessTokensAllowed(context)) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== navigation.revision || now.route !== navigation.route || now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    close(); setNotice('');
    let scope: NativeAccessTokenScope;
    try {scope = createNativeAccessTokenScope(context, lifetime);} catch (failure) {if ((failure as {name?: string})?.name === 'AbortError') return; throw failure;}
    const transport: ApiAccessTransport = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => scope.request<T>(path, method ?? 'GET', body, signal)};
    const held = {scope, transport};
    openingRef.current = held; setOpening(held); load(held);
  };
  const held = opening;
  const editing = () => current(held) && !busyRef.current;
  const edit = (setter: (value: string) => void) => (value: string) => {if (editing()) setter(value);};
  const toggleScope = (scope: ApiScope) => {if (!editing()) return; setTicked(current => {
    const next = new Set(current);
    if (scope === 'admin') {if (next.has('admin')) next.delete('admin'); else {next.clear(); next.add('admin');} return next;}
    next.delete('admin'); if (next.has(scope)) next.delete(scope); else next.add(scope); return next;
  });};
  const begin = () => {if (!editing()) return; setView('create'); setError(undefined);};
  const submit = () => {
    const attempt = held, chosen = chosenScopes(ticked), trimmed = name.trim();
    if (!current(attempt) || busyRef.current || !trimmed || !chosen.length) return;
    const expiresAt = expiryInstant(days, Date.now());
    // One operation id per draft: a retry of the same ask never makes a second token.
    const operationId = operations.current.forPayload(JSON.stringify([trimmed, chosen, expiresAt ?? '']));
    void run(attempt, h => createAccessToken(h.transport, {operationId, name: trimmed, scopes: chosen, ...(expiresAt ? {expiresAt} : {})}, h.scope.signal), receipt => {
      operations.current.release();
      setIssued(receipt); setName(''); setTicked(new Set()); setDays(0); setView('receipt');
    });
  };
  const revoke = () => {
    const attempt = held, target = revoking;
    if (!current(attempt) || !target || busyRef.current) return;
    void run(attempt, h => revokeAccessToken(h.transport, target.id, h.scope.signal), () => {
      setRevoking(undefined); setView('list'); setNotice(t('api.credential.tokenRevoked'));
    }).then(() => load(attempt));
  };
  const copySecret = async () => {
    const secret = issued?.secret;
    if (!secret || !current(held) || copyRef.current) return;
    copyRef.current = true;
    try {await copyText(secret); if (current(held)) {setCopied(true); setError(undefined);}}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (openingRef.current === held) copyRef.current = false;}
  };
  if (!lifetime.current() || !context.current() || !nativeAccessTokensAllowed(context) || theme.isTV) return null;
  const receipt = view === 'receipt';
  const title = receipt ? t('api.tokens.copyTitle') : view === 'create' ? t('api.tokens.createTitle') : view === 'revoke' ? t('web.access.revokeTitle', {name: revoking?.name ?? ''}) : t('api.tokens.title');
  const bodyText = (value: string) => <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{value}</Text>;
  const retry = () => {if (current(held)) {setError(undefined); load(held);}};
  const failure = error !== undefined ? <StateView testID="access-token-error" {...runtime.error(error, retry, 'account')}/> : null;
  const scopeRow = (scope: ApiScope) => <Switch key={scope} testID={`access-token-scope-${scope}`} checked={ticked.has(scope)} disabled={busy} description={t(`api.scope.${scope}.help` as MessageId)} onCheckedChange={() => toggleScope(scope)}>{t(`api.scope.${scope}` as MessageId)}</Switch>;
  const content = view === 'list'
    ? <View style={{gap: 12}}>{failure}
        {options && !options.tokensAllowed ? bodyText(t('api.tokens.off')) : null}
        {busy && !page ? bodyText(t('status.loading')) : null}
        {page && !page.items.length ? bodyText(t('api.tokens.none')) : null}
        {page?.items.length ? <View>{page.items.map((token, index) => <SettingsRow key={token.id} first={index === 0} testID={`access-token-${token.id}`} label={token.name}
          description={[token.scopes.map(scope => t(`api.scope.${scope}` as MessageId)).join(', '), t('api.credential.created', {when: defaultI18n.date(token.createdAt)}), token.lastUsedAt ? t('api.credential.lastUsed', {when: defaultI18n.date(token.lastUsedAt)}) : t('api.credential.neverUsed')].join(' · ')}
          value={token.state === 'active' ? undefined : t(`api.credential.state.${token.state}` as MessageId)}
          onPress={credentialWorks(token) ? () => {if (current(held) && !busyRef.current) {setRevoking(token); setView('revoke'); setError(undefined);}} : undefined}/>)}</View> : null}
        {page?.nextCursor ? <Button size="compact" variant="glass" disabled={busy} onPress={() => {if (current(held) && !busyRef.current && page?.nextCursor) load(held, page.nextCursor, true);}}>{t('action.showMore')}</Button> : null}
      </View>
    : view === 'create'
    ? <View style={{gap: 12}}>{failure}
        <TextField testID="access-token-name" label={t('builder.name')} description={t('api.tokens.nameHelp')} value={name} onChangeText={edit(setName)} maxLength={80} editable={!busy}/>
        <View style={{gap: 8}}>{apiScopes.filter(scope => !options || options.scopes.includes(scope)).map(scopeRow)}</View>
        <Select testID="access-token-expiry" label={t('api.credential.expiryLabel')} value={String(days)} disabled={busy} options={EXPIRY_DAYS.map(value => ({value: String(value), label: expiryLabel(t, value)}))} onValueChange={value => {const parsed = Number(value); if (editing() && EXPIRY_DAYS.some(day => day === parsed)) setDays(parsed as ExpiryDays);}}/>
      </View>
    : view === 'revoke'
    ? <View style={{gap: 12}}>{failure}{bodyText(t('api.tokens.revokeBody'))}</View>
    : <View style={{gap: 12}}>{failure}
        {issued?.secret ? <Text testID="access-token-secret" selectable style={[shellType(16, 700, 24), {color: theme.colors.text}]}>{issued.secret}</Text> : bodyText(t('api.credential.secretNotShown'))}
        {copied ? bodyText(t('api.credential.copied')) : null}
      </View>;
  const footer = receipt
    ? <><Button testID="access-token-copy" variant="glass" disabled={!issued?.secret} onPress={() => void copySecret()}>{t('web.twoStep.copy')}</Button><Button testID="access-token-copied" onPress={() => {if (current(held)) close();}}>{t('web.access.copiedIt')}</Button></>
    : view === 'create'
    ? <><Button variant="glass" disabled={busy} onPress={() => {if (editing()) {setView('list'); setError(undefined);}}}>{t('action.back')}</Button><Button testID="access-token-create" loading={busy} disabled={busy || !name.trim() || !chosenScopes(ticked).length} onPress={submit}>{t('api.tokens.create')}</Button></>
    : view === 'revoke'
    ? <><Button variant="glass" disabled={busy} onPress={() => {if (editing()) {setRevoking(undefined); setView('list');}}}>{t('action.cancel')}</Button><Button testID="access-token-revoke" variant="danger" loading={busy} disabled={busy} onPress={revoke}>{t('web.access.revoke')}</Button></>
    : <><Button variant="glass" onPress={() => {if (current(held)) close();}}>{t('action.close')}</Button>{options?.tokensAllowed !== false && <Button testID="access-token-add" icon="i-plus" disabled={busy || !options} onPress={begin}>{t('api.tokens.create')}</Button>}</>;
  const surface = {
    testID: 'access-tokens', open: !!opening && current(opening), title,
    onOpenChange: (value: boolean) => {if (!value && !receipt && current(held)) close();},
    dismissOnBack: !receipt, dismissOnOutsidePress: !receipt, dismissOnGesture: !receipt, showClose: !receipt,
    footer: <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{footer}</View>,
  };
  return <>
    <SettingsRow first testID="settings-panel-access-tokens" label={t('api.tokens.title')} onPress={open}/>
    {notice && <Text accessibilityLiveRegion="polite" style={[shellType(13, 400, 19), {color: theme.colors.textSecondary, padding: 14}]}>{notice}</Text>}
    {theme.platform === 'phone' ? <BottomSheet {...surface} fitContent keyboardAvoiding>{content}</BottomSheet>
      : <Dialog {...surface}><ScrollView keyboardShouldPersistTaps="handled" style={{maxHeight: Math.max(180, dimensions.height * .55)}}>{content}</ScrollView></Dialog>}
  </>;
}
