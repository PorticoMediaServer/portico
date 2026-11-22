import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Dimensions, Platform} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {SetupPreview} from '../../../../../../packages/client-core/src/setup-code';
import type {TVConsentApprover} from '../../../../../../packages/client-core/src/tv-consent';
import type {AppServerSession} from '../../../../../../packages/client-core/src/app-sessions';
import {nativeAccounts} from '../../../platform/accounts';
import {nativeAppSessions, subscribeNativeAppSessions} from '../../../platform/app-session-storage';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeServersPanel} from '../../../shell/routes';
import {offlineDownloads, offlineOwnerKey} from '../../../../platform/offline-downloads';
import {useTheme} from '../../../kit/theme';
import {registerBack} from '../../../kit/navigation/back';
import {createAccountDownloadsEntry, type AccountDownloadsEntry} from '../../../screens/accounts/downloads-entry';
import {completeTVLinkCode, formatTVLinkCode} from '../../../screens/accounts/account-form-values';
import {removeCapturedServerDownloads} from '../../../screens/accounts/sign-out-downloads';

const t = defaultI18n.t;
export type ServersPanel = 'servers' | 'add' | 'sign-out-server' | 'forget-server' | 'sign-out-account' | 'consent-kind' | 'consent-code' | 'consent-review' | 'consent-done';

/** `embedded`: a TV's Switch server Settings page (connect-24) draws the chooser inside Settings, which owns Back; Use a local
 * server and Sign out of Portico Account open this route on their own panel (`openPanel`), and leaving that panel leaves the route. */
export type ServersRouteProps = {
  context?: NativeShellBootstrapContext; fallbackDownloads?: AccountDownloadsEntry; initialPanel?: 'servers' | NativeServersPanel;
  embedded?: boolean; openPanel?: (panel: NativeServersPanel) => void; onBack?: () => void; onComplete?: () => void;
  onSetupComplete?: (serverId: string) => void;
};
type SavedServer = {id: string; familyId: string; fingerprint: string; name: string};

/** Every saved server, Sign out and Forget, Use a local server, Link a TV and Sign out of Portico Account: the panel shown,
 * each action and its guard. Moved from ServersRoute as it was; the page only draws. */
export function useServersRoute(props: ServersRouteProps) {
  const {context, fallbackDownloads, initialPanel = 'servers', embedded = false, openPanel, onBack, onComplete} = props;
  const theme = useTheme(), [panel, setPanel] = useState<ServersPanel>(initialPanel), [servers, setServers] = useState<Awaited<ReturnType<typeof nativeAccounts.snapshot>>>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
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
  // Whether this app holds a Portico Account is known only once the stored session has been read.
  const [accountChecked, setAccountChecked] = useState(false);
  useEffect(() => {
    let live = true;
    void accountCredentials.synchronize().catch(() => {}).finally(() => {if (live) setAccountChecked(true);});
    return () => {live = false;};
  }, [accountCredentials]);
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
  const errorBody = error ? presentError(error, 'account').body : undefined;
  const secondaryAction = panel !== 'servers' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: cancel} : undefined;

  const addDownloads = addCurrent() ? committedAdd?.downloads : undefined;
  const addBack = () => {if (addCurrent()) close();};
  const addComplete = () => {
    // Successful publication can retire the borrowed Downloads entry before this callback.
    if (!alive.current || panelRef.current !== 'add' || !addOwner.current || addIntent.current.generation !== addGeneration || addIntent.current.context !== context) return;
    close(); onComplete?.();
  };
  const signOutAccountAction = action('sign-out-account', t('action.signOut'), async () => {
    if (!accountScope) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
    await nativeAccounts.signOutPorticoAccount(accountScope); close(); onComplete?.();
  });
  const localConsentOffered = !!context && servers?.servers.find(value => value.id === servers.selectedServerId)?.signIn === 'local';
  // Neither way to approve a TV is open: no local sign-in on this server and no Portico Account. Said only once both are known,
  // so it never flashes for someone who does hold an account.
  const consentUnavailable = !localConsentOffered && !accountHeld && accountChecked && servers !== undefined;
  const chooseLocalConsent = () => {void run(async () => {
    approver.current = await nativeAccounts.localTVConsent(context!.api, context!.session); setConsentKind('local'); setPanel('consent-code');
  });};
  const choosePorticoConsent = () => {void run(async () => {approver.current = await nativeAccounts.porticoTVConsent(); setConsentKind('portico'); setPanel('consent-code');});};
  const changeCode = (value: string) => setCode(formatTVLinkCode(value));
  const reviewAction = action('review-tv', t('web.device.review'), async () => {setPreview(await approver.current!.review(code)); setPanel('consent-review');}, !completeTVLinkCode(code));
  // A platform in the catalogue's own words (device.platform.android → "Android"); one it doesn't name is shown as sent.
  const platformName = (platform: string) => {const key = `device.platform.${platform.toLowerCase()}`;
    return defaultI18n.has?.(key) ? defaultI18n.t(key as Parameters<typeof defaultI18n.t>[0]) : platform;};
  // The device's own facts, labelled as the Services review labels them; the platform in its own words ("Android", not "android").
  const previewFacts = preview ? [
    {label: t('services.device.device'), value: preview.deviceName}, {label: t('services.device.platform'), value: platformName(preview.platform)},
    {label: t('about.appVersion'), value: preview.appVersion}, {label: t('auth.code'), value: preview.userCode},
    {label: t('accounts.settings.title'), value: t(consentKind === 'portico' ? 'accounts.session.portico' : 'accounts.session.local')},
  ].filter(fact => fact.value) : [];
  // Web's words for any device ("Sign Pixel 6a in?"), not "Is this your TV?".
  const reviewTitle = preview ? t('next.cast.linkTvReview', {device: preview.deviceName}) : t('linkTV.askConfirm');
  const approveAction = action('consent-approve', t('web.device.signItIn'), async () => {await approver.current!.decide(preview!, 'approve'); setDone(t('linkTV.approved', {device: preview!.deviceName})); setPanel('consent-done');});
  const deny = () => {void run(async () => {await approver.current!.decide(preview!, 'deny'); setDone(t('linkTV.declined')); setPanel('consent-done');});};
  const doneAction = action('done', t('action.done'), async () => close());

  const deviceNoun = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  const selectedServer = !!captured && servers?.selectedServerId === captured.session.connection.pin.serverId;
  // Sign out and Forget share one confirmation: the same downloads choice, a different title, line and action.
  const forgetting = panel === 'forget-server';
  const dismissSheet = () => {setRemoveDownloads(false); close();};
  const confirmSheet = () => {if (!captured) return; void run(async () => {
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
  });};
  const sheet = captured ? {open: panel === 'sign-out-server' || forgetting, forgetting, selectedServer, removeDownloads,
    title: forgetting ? t('connection.identity.forgetTitle', {server: captured.name}) : t('settings.signOutConfirm.title', {server: captured.name, device: t(deviceNoun)}),
    confirmLabel: forgetting ? t('connection.identity.forgetAction') : t('action.signOut'),
    forgetBody: forgetting ? t('accounts.servers.forgetBody', {server: captured.name}) : undefined,
    toggleRemove: () => setRemoveDownloads(value => !value), confirm: confirmSheet, dismiss: dismissSheet} : undefined;

  const capture = (next: 'sign-out-server' | 'forget-server') => (value: SavedServer) => {void run(async () => {
    const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
    if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
      throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
    setCaptured({session: found, name: value.name}); setPanel(next);
  });};
  const select = (value: {id: string}) => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});};
  const refresh = () => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});};
  const add = () => {void run(async () => {openAdd();});};
  const linkTV = () => setPanel('consent-kind');
  const signOutAccount = async (open: (session: {accountId: string; familyId: string}) => void) => {
    const credentials = nativeAccounts.accountCredentials();
    await credentials.synchronize();
    const session = credentials.getSnapshot().session;
    if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
    open({accountId: session.account.id, familyId: session.familyId});
  };
  const openSignOutAccount = (scope: {accountId: string; familyId: string}) => {setAccountScope(scope); setPanel('sign-out-account');};
  const signOutPortico = () => {void run(() => signOutAccount(openSignOutAccount));};

  // On a TV's Switch server Settings page (embedded), the panels that fill the screen open this route on their own panel.
  // They open outside run(): a busy turn ending would ask for the chooser's start while the opened route is on top.
  const opens = embedded ? openPanel : undefined;
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
  const tvAdd = opens ? () => opens('add') : add;
  const tvLinkTV = opens ? () => opens('consent-kind') : linkTV;
  const tvSignOutPortico = opens ? () => signOutFromSettings(opens) : signOutPortico;

  return {tv: theme.isTV, panel, busy, errorBody, secondaryAction, back: onBack ? cancel : undefined, addGeneration, addDownloads, addBack, addComplete,
    signOutAccountAction, localConsentOffered, consentUnavailable, consentPortico: consentKind === 'portico', chooseLocalConsent, choosePorticoConsent, code, changeCode, reviewAction, previewFacts, reviewTitle, approveAction, deny,
    done, doneAction, sheet, servers, select, signOut: capture('sign-out-server'), forget: capture('forget-server'), refresh, add, linkTV, signOutPortico,
    accountHeld, owner, tvAdd, tvLinkTV, tvSignOutPortico};
}
