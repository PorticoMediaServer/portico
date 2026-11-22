import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Dimensions, Platform, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {SetupPreview} from '../../../../../packages/client-core/src/setup-code';
import type {TVConsentApprover} from '../../../../../packages/client-core/src/tv-consent';
import type {AppServerSession} from '../../../../../packages/client-core/src/app-sessions';
import {nativeAccounts} from '../../platform/accounts';
import {nativeAppSessions, subscribeNativeAppSessions} from '../../platform/app-session-storage';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeServersPanel, NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {TVAccountFields, TVAuthLayout} from '../../kit/frames/TVAuthLayout';
import {StateLayout} from '../../kit/states';
import {Button, Focusable, TextField, FormError} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {offlineDownloads, offlineOwnerKey} from '../../../platform/offline-downloads';
import {useTheme} from '../../kit/theme';
import {AccountEntry} from './AccountEntry';
import {createAccountDownloadsEntry, type AccountDownloadsEntry} from './downloads-entry';
import {completeTVLinkCode, formatTVLinkCode} from './account-form-values';
import {ServerChooserPage} from '../../pages/touch/accounts/ServerChooserPage';
import {TrustedCertificatesPage} from '../../pages/shared/accounts/TrustedCertificatesPage';
import {TVServerChooserPage, TVServerSettingsPanel} from '../../pages/tv/accounts/ServerChooserPage';
import {removeCapturedServerDownloads} from './sign-out-downloads';
import {registerBack} from '../../kit/navigation/back';
import {ProfilesRoutePage} from '../../pages/shared/accounts/ProfilesRoutePage';

const t = defaultI18n.t;
type Panel = 'servers' | 'add' | 'sign-out-server' | 'forget-server' | 'sign-out-account' | 'consent-kind' | 'consent-code' | 'consent-review' | 'consent-done';

/** Genuine account destinations for the current authority. Shell media routes and Home
 * remain with their existing owners. Runtime disposal retires every private view below. */
export function AccountRoute({route, context}: {route: NativeShellRoute; context: NativeShellBootstrapContext}) {
  const back = () => {context.navigation.back(context.authority());};
  return route.kind === 'profiles' ? <ProfileRoute context={context} route={route} onBack={back}/> : <ServersRoute context={context} initialPanel={route.kind === 'servers' ? route.initialPanel : undefined} onBack={back}/>;
}

/** Moved to a page; kept under this name for Settings until the rest of this file moves. */
export const ProfileRoute = ProfilesRoutePage;

/** Also usable when no selected server can restore: retained servers remain reachable. */
/** `embedded`: a TV's Switch server Settings page (connect-24) draws the chooser inside Settings, which owns Back; Use a local
 * server and Sign out of Portico Account open this route on their own panel (`openPanel`), and leaving that panel leaves the route. */
export function ServersRoute(props: {
  context?: NativeShellBootstrapContext; fallbackDownloads?: AccountDownloadsEntry; initialPanel?: 'servers' | NativeServersPanel;
  embedded?: boolean; openPanel?: (panel: NativeServersPanel) => void; onBack?: () => void; onComplete?: () => void;
  onSetupComplete?: (serverId: string) => void;
}) {
  const {context, fallbackDownloads, initialPanel = 'servers', embedded = false, openPanel, onBack, onComplete, onSetupComplete} = props;
  const theme = useTheme(), [panel, setPanel] = useState<Panel>(initialPanel), [servers, setServers] = useState<Awaited<ReturnType<typeof nativeAccounts.snapshot>>>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [captured, setCaptured] = useState<{session: AppServerSession; name: string}>(), [code, setCode] = useState('');
  // Opened straight on Sign out of Portico Account (from a TV's Settings page): the account signed in now is the one asked about.
  const [accountScope, setAccountScope] = useState<{accountId: string; familyId: string} | undefined>(() => {
    if (initialPanel !== 'sign-out-account') return undefined;
    const session = nativeAccounts.accountCredentials().getSnapshot().session;
    return session ? {accountId: session.account.id, familyId: session.familyId} : undefined;
  });
  const [preview, setPreview] = useState<Readonly<SetupPreview>>(), [consentKind, setConsentKind] = useState<'local' | 'portico'>(), [done, setDone] = useState('');
  const [removeDownloads, setRemoveDownloads] = useState(false);
  // Embedded: a second press of Sign out of Portico Account while the account is being checked opens nothing more.
  const openingSignOut = useRef(false);
  const [addGeneration, setAddGeneration] = useState(0);
  // "Sign out of Portico Account" is offered whenever this app holds one, whatever servers are saved (ruling, 13 Nov).
  const accountCredentials = nativeAccounts.accountCredentials();
  const accountHeld = useSyncExternalStore(accountCredentials.subscribe, () => accountCredentials.getSnapshot().session !== undefined, () => false);
  useEffect(() => {void accountCredentials.synchronize().catch(() => {});}, [accountCredentials]);
  type AddOpening = {active: boolean; generation: number; context?: NativeShellBootstrapContext; fallback?: AccountDownloadsEntry; downloads?: AccountDownloadsEntry; disposeDownloads?: () => void};
  const addOwner = useRef<AddOpening | undefined>(undefined), [committedAdd, setCommittedAdd] = useState<AddOpening>();
  const panelRef = useRef(panel), addIntent = useRef({generation: addGeneration, context});
  panelRef.current = panel; addIntent.current = {generation: addGeneration, context};
  if (addOwner.current && (panel !== 'add' || addOwner.current.context !== context || addOwner.current.generation !== addGeneration || addOwner.current.fallback !== fallbackDownloads)) addOwner.current.active = false;
  useLayoutEffect(() => {
    if (panel !== 'add') return;
    const opening: AddOpening = {active: true, generation: addGeneration, context, fallback: fallbackDownloads}; addOwner.current = opening;
    if (context && !Platform.isTV) {try {opening.downloads = createAccountDownloadsEntry(context,
      () => opening.active && addOwner.current === opening && panelRef.current === 'add'); opening.disposeDownloads = opening.downloads.dispose;} catch {opening.downloads = undefined;}}
    if (!context && !Platform.isTV) opening.downloads = fallbackDownloads;
    setCommittedAdd(opening);
    return () => {opening.active = false; if (addOwner.current === opening) addOwner.current = undefined; opening.disposeDownloads?.();};
  }, [panel, context, addGeneration, fallbackDownloads]);
  const addCurrent = () => !!committedAdd?.active && addOwner.current === committedAdd && panelRef.current === 'add';
  const retireAdd = () => {const opening = addOwner.current; addOwner.current = undefined; if (opening) {opening.active = false; opening.disposeDownloads?.();}};
  const openAdd = () => {if (panelRef.current !== 'servers') return; retireAdd(); setAddGeneration(value => value + 1); setPanel('add');};
  const approver = useRef<TVConsentApprover | undefined>(undefined), alive = useRef(true);
  useEffect(() => {alive.current = true; let read = 0;
    const update = () => {const owned = ++read; void nativeAccounts.snapshot().then(value => {if (alive.current && owned === read) setServers(value);}, failure => {if (alive.current) setError(failure);});};
    update(); const off = subscribeNativeAppSessions(update);
    return () => {alive.current = false; off(); approver.current?.dispose();};
  }, []);
  const run = async (work: () => Promise<void>) => {if (busy || context && !context.current()) return; setBusy(true); setError(undefined);
    try {await work();} catch (failure) {if (alive.current) setError(failure);} finally {if (alive.current) setBusy(false);}
  };
  // A route opened on Use a local server or Sign out of Portico Account from Settings goes back there, not to a second chooser.
  const leaveOnClose = initialPanel === 'add' || initialPanel === 'sign-out-account'
    || Platform.isTV && initialPanel === 'consent-kind';
  const close = () => {
    retireAdd(); approver.current?.dispose(); approver.current = undefined;
    setPreview(undefined); setCode(''); setError(undefined);
    if (leaveOnClose) onBack?.(); else setPanel('servers');};
  const cancel = () => {if (panel === 'servers') onBack?.(); else close();};
  useEffect(() => {if (panel === 'add' || embedded && panel === 'servers') return; const subscription = registerBack(() => {
    if (busy) return true;
    if (panel === 'servers' && !onBack) return false;
    cancel(); return true;
  }, {scope: 'surface'}); return () => subscription();});
  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const errorView = error ? <FormError placement="block">{presentError(error, 'account').body}</FormError> : null;
  const AccountLayout = theme.isTV ? TVAuthLayout : StateLayout;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<typeof action>, more?: React.ReactNode) => <AccountLayout testID="accounts-servers" title={title} body={body}
    beforeActions={<>{before}{errorView}</>} primaryAction={primary} secondaryAction={panel !== 'servers' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: cancel} : undefined}>{more}</AccountLayout>;
  if (panel === 'add') return <AccountEntry key={addGeneration} downloads={addCurrent() ? committedAdd?.downloads : undefined} onSetupComplete={onSetupComplete} initialStage="address" onBack={() => {if (addCurrent()) close();}} onComplete={() => {
    // Successful publication can retire the borrowed Downloads entry before this callback.
    if (!alive.current || panelRef.current !== 'add' || !addOwner.current || addIntent.current.generation !== addGeneration || addIntent.current.context !== context) return;
    close(); onComplete?.();
  }}/>;
  if (panel === 'sign-out-account') return layout(t('accounts.signOut.portico'), undefined, undefined,
    action('sign-out-account', t('action.signOut'), async () => {
      if (!accountScope) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
      await nativeAccounts.signOutPorticoAccount(accountScope); close(); onComplete?.();
    }));
  if (panel === 'consent-kind') return layout(t('settings.linkTV'), t('linkTV.enterCode'), <View style={{gap: 12, alignSelf: 'stretch', marginBottom: 20}}>
    {context && servers?.servers.find(value => value.id === servers.selectedServerId)?.signIn === 'local' && <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {
      approver.current = await nativeAccounts.localTVConsent(context.api, context.session); setConsentKind('local'); setPanel('consent-code');
    });}}>{t('accounts.session.local')}</Button>}
    <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {approver.current = await nativeAccounts.porticoTVConsent(); setConsentKind('portico'); setPanel('consent-code');});}}>{t('account.portico')}</Button>
  </View>);
  if (panel === 'consent-code') {
    const codeField = <TextField label={t('linkTV.enterCode')} value={code} onChangeText={value => setCode(formatTVLinkCode(value))}
      autoCapitalize="characters" maxLength={9} autoCorrect={false} editable={!busy}/>;
    return layout(t('settings.linkTV'), t(consentKind === 'portico' ? 'accounts.consent.portico' : 'accounts.consent.local'),
      theme.isTV ? <TVAccountFields>{codeField}</TVAccountFields> : <View style={{marginBottom: 20}}>{codeField}</View>,
      action('review-tv', t('web.device.review'), async () => {setPreview(await approver.current!.review(code)); setPanel('consent-review');}, !completeTVLinkCode(code)));
  }
  if (panel === 'consent-review') return layout(t('linkTV.askConfirm'), t('web.device.approveWarn'),
    <View style={{marginBottom: 20}}>{[preview!.deviceName, preview!.platform, preview!.appVersion, preview!.userCode, t(consentKind === 'portico' ? 'accounts.session.portico' : 'accounts.session.local')].map((value, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.text}]}>{value}</Text>)}</View>,
    action('consent-approve', t('web.device.signItIn'), async () => {await approver.current!.decide(preview!, 'approve'); setDone(t('linkTV.approved', {device: preview!.deviceName})); setPanel('consent-done');}),
    <Button variant="text" disabled={busy} onPress={() => {void run(async () => {await approver.current!.decide(preview!, 'deny'); setDone(t('linkTV.declined')); setPanel('consent-done');});}}>{t('web.device.notMine')}</Button>);
  if (panel === 'consent-done') return layout(done, undefined, undefined, action('done', t('action.done'), async () => close()));
  const deviceNoun = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  const selectedServer = !!captured && servers?.selectedServerId === captured.session.connection.pin.serverId;
  // Sign out and Forget share one confirmation: the same downloads choice, a different title, line and action.
  const forgetting = panel === 'forget-server';
  const signOutSheet = captured ? <BottomSheet open={panel === 'sign-out-server' || forgetting} onOpenChange={open => {if (!open) {setRemoveDownloads(false); close();}}} testID="accounts-sign-out-sheet"
    title={forgetting ? t('connection.identity.forgetTitle', {server: captured.name}) : t('settings.signOutConfirm.title', {server: captured.name, device: t(deviceNoun)})}
    footer={<View style={{gap: 10}}>
      <Focusable focusId="sign-out-confirm" accessibilityRole="button" accessibilityLabel={forgetting ? t('connection.identity.forgetAction') : t('action.signOut')} disabled={busy}
        onPress={() => {void run(async () => {
          if (removeDownloads && selectedServer) await removeCapturedServerDownloads(captured, {
            selected: () => nativeAppSessions.selected(),
            entries: () => offlineDownloads.getSnapshot().entries,
            remove: async entry => {
              const owner = offlineOwnerKey(captured.session.connection.session.viewer);
              const live = offlineDownloads.getSnapshot().entries.find(value => value.id === entry.id
                && (value.receipt ? offlineOwnerKey(value.receipt.claims.viewer) : value.owner) === owner);
              if (live) await offlineDownloads.remove(live);
            },
          });
          const pin = captured.session.connection.pin, familyId = captured.session.connection.session.sessionFamilyId;
          // Forget is local and never contacts the server, so it works for a sign-in that ended or a server that's gone.
          if (forgetting) await nativeAccounts.forgetServer({serverId: pin.serverId, familyId, fingerprint: pin.fingerprint});
          else await nativeAccounts.signOutServer(pin.serverId, familyId);
          setRemoveDownloads(false); close(); onComplete?.();
        });}} focusRadius={12} focusTreatment="none"
        style={{height: 54, borderRadius: 12, backgroundColor: `${theme.colors.danger}24`, alignItems: 'center', justifyContent: 'center'}}>
        <Text style={[theme.type.button, {color: theme.colors.danger}]}>{forgetting ? t('connection.identity.forgetAction') : t('action.signOut')}</Text>
      </Focusable>
      <Button focusId="sign-out-cancel" variant="text" disabled={busy} onPress={() => {setRemoveDownloads(false); close();}} textStyle={{color: theme.colors.textSecondary}}>{t('action.cancel')}</Button>
    </View>}>
    {forgetting && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 6}]}>{t('accounts.servers.forgetBody', {server: captured.name})}</Text>}
    <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 14}]}>{t('settings.signOutConfirm.bodyDownloads')}</Text>
    {selectedServer && <Focusable focusId="sign-out-remove-downloads" accessibilityRole="checkbox" accessibilityState={{checked: removeDownloads}} accessibilityLabel={t('settings.signOutConfirm.removeDownloads')}
      onPress={() => setRemoveDownloads(value => !value)} focusRadius={6} focusTreatment="none" style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 8}}>
      <View style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
        borderColor: removeDownloads ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: removeDownloads ? theme.colors.accentStrong : 'transparent'}}>
        {removeDownloads && <Icon name="i-check" size={16} color={theme.colors.ink}/>}
      </View>
      <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{t('settings.signOutConfirm.removeDownloads')}</Text>
    </Focusable>}
  </BottomSheet> : null;
  if (!Platform.isTV) return <><ServerChooserPage certificates={<TrustedCertificatesPage context={context}/>} snapshot={servers} busy={busy} error={errorView} onBack={onBack ? cancel : undefined}
    onSelect={value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});}}
    onSignOut={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });}}
    onForget={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('forget-server');
    });}}
    onRefresh={() => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});}}
    onAdd={() => {void run(async () => {openAdd();});}}
    footer={<View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button variant="text" disabled={busy} onPress={() => setPanel('consent-kind')}>{t('settings.linkTV')}</Button>
      {accountHeld && <Button variant="text" disabled={busy} onPress={() => {void run(async () => {
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize(); const session = credentials.getSnapshot().session;
        if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
        setAccountScope({accountId: session.account.id, familyId: session.familyId}); setPanel('sign-out-account');
      });}}>{t('accounts.signOut.portico')}</Button>}
    </View>}/>{signOutSheet}</>;
  // On a TV's Switch server Settings page (embedded), the panels that fill the screen open this route on their own panel.
  // They open outside run(): a busy turn ending would ask for the chooser's start while the opened route is on top.
  const opens = embedded ? openPanel : undefined;
  const signOutAccount = async (open: (session: {accountId: string; familyId: string}) => void) => {
    const credentials = nativeAccounts.accountCredentials();
    await credentials.synchronize();
    const session = credentials.getSnapshot().session;
    if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
    open({accountId: session.account.id, familyId: session.familyId});
  };
  const openSignOutAccount = (scope: {accountId: string; familyId: string}) => {setAccountScope(scope); setPanel('sign-out-account');};
  // Embedded: the route opened on Sign out of Portico Account reads the scope itself once this has synchronized.
  const signOutFromSettings = (open: (panel: NativeServersPanel) => void) => {
    if (openingSignOut.current) return;
    openingSignOut.current = true;
    setError(undefined);
    signOutAccount(() => {if (alive.current && (!context || context.current())) open('sign-out-account');})
      .catch(failure => {if (alive.current) setError(failure);})
      .finally(() => {openingSignOut.current = false;});
  };
  const owner = !!context && context.current() && context.account.serverId === servers?.selectedServerId
    && context.account.account.role === 'owner';
  const tvProps: React.ComponentProps<typeof TVServerChooserPage> = {snapshot: servers, busy, error: errorView,
    onBack: onBack ? cancel : undefined, owner,
    onSelect: value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});},
    onSignOut: value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });},
    onForget: value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('forget-server');
    });},
    onRefresh: () => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});},
    onAdd: opens ? () => opens('add') : () => {void run(async () => {openAdd();});},
    certificates: (bind, refreshServers) => <TrustedCertificatesPage context={context} refreshRef={bind('certificates-refresh')}
      refreshDown={refreshServers}/>,
    footer: bind => <View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button ref={bind('link-tv')} focusId="link-tv" variant="link" disabled={busy}
        onPress={opens ? () => opens('consent-kind') : () => setPanel('consent-kind')}>
        {t('settings.linkTV')}
      </Button>
      {accountHeld && <Button focusId="sign-out-portico" variant="link" disabled={busy}
        onPress={opens ? () => signOutFromSettings(opens) : () => {void run(() => signOutAccount(openSignOutAccount));}}>
        {t('accounts.signOut.portico')}
      </Button>}
    </View>};
  return <>{embedded ? <TVServerSettingsPanel {...tvProps}/> : <TVServerChooserPage {...tvProps}/>}{signOutSheet}</>;
}

const createAccountRoute: NativeRouteFactory = context => ({render: route => {
  if (route.kind !== 'profiles' && route.kind !== 'servers') throw new Error('native_account_route_mismatch');
  return <AccountRoute key={context.navigation.getSnapshot().revision} route={route} context={context}/>;
}});
nativeShellRoutes.registerRoute('profiles', createAccountRoute);
nativeShellRoutes.registerRoute('servers', createAccountRoute);
