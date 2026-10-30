import {dependsOn} from '@core/freshness-hub.ts';
import {forwardFacts, subscribeFacts} from '../../shell/fact-source';
import {mountQuietInput, type QuietInput} from './quiet-refresh';
import {Fragment, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {useRouterState} from '@tanstack/react-router';
import {peopleStreamLimits, settingsBinding} from '@core/presentation/settings-bindings.ts';
import {settingsRowIndex, settingsSpec} from '@core/presentation/settings-spec.ts';
import {captureReportContext, type ReportContext} from '@core/report-context.ts';
import {SettingsPeopleClient} from '@core/settings-people.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {shellLanguage} from '../../shell/settings';
import {settingsRoute} from '../../kit/settings/search';
import {SettingsPageFailure} from '../../kit/settings';
import {settingsPlatform} from '../../platform/settings-platform';
import {DashboardScreen, HistoryScreen, createDashboardBindings, type DashboardBindings} from '../dashboard';
import {SettingsScreen, type SettingsBindings, type SettingsContext, type SettingsPageSnapshot} from './Settings';
import {SecureAddressProvider} from './SecureAddressProvider';
import {CustomizeHomeRow} from './account-appearance/CustomizeHome';
import {SettingsController, type SettingsControllerOptions} from './controller';
import {createSettingsBindings, forwardSettingsBindings} from './bindings';
import {LibraryManagementService} from '@core/library-management.ts';
import {LibrariesPanel} from './libraries/LibrariesPanel';
import {MetadataSource} from './libraries/MetadataSource';
import {LibraryHeader} from './libraries/LibraryHeader';
import {RenameLibrary} from './libraries/RenameLibrary';
import {MissingRetention} from './libraries/MissingRetention';
import {LibraryScanAction} from './libraries/LibraryActions';
import {LibraryLyricsAction} from './libraries/LibraryLyricsAction';
import {RemoveLibraryButton} from './libraries/RemoveLibrary';
import {isLibraryPanelContext, libraryScanAction} from './libraries/model';
import {createServerSettingsSurfaces} from './server';
import {createTroubleshootingSurfaces} from './server/troubleshooting';
import {MessageOptions} from './server/troubleshooting/MessageOptions';
import {PlaybackProblems} from './server/troubleshooting/PlaybackProblems';
import {PlaybackProblemsSource} from './server/troubleshooting/playback-problems-source';
import {IntegrationsPage} from './server/integrations/ApiKeys';
import {SettingsApiKeysSource} from './server/integrations/api-keys-source';
import {SettingsWebhooksSource} from './server/integrations/webhooks-source';
import {MissingTitles} from './server/MissingTitles';
import {SettingsMissingSource} from './server/missing-source';
import {SettingsUpdatesSource} from './server/updates-source';
import {UpdatesCheckNow, UpdatesCurrent} from './server/GeneralUpdates';
import {GeneralMessage, GeneralRestart} from './server/GeneralTools';
import {SettingsHardwareSource} from './hardware/hardware-source';
import {HardwareDevices, TestHardware, TranscodeFolder} from './hardware/HardwareRows';
import {hardwareEligible, hardwareRowIds, hardwareRowState} from './hardware/hardware-rows';
import {hardwareSelectionChoices} from '@core/presentation/hardware-selection.ts';
import {presetEnginesAvailable} from './server/engines';
import {LibraryFolders} from './libraries/LibraryFolders';
import {SkippedFiles} from './libraries/SkippedFiles';
import {readingSettingsAdapter} from './reading-adapter';
import {hasWebPreferenceEffect} from './effects';
import {createPrivacySettingsSurfaces} from './privacy-controls';
import {PeopleScreen, type PeopleBindings} from './People';
import {createPeopleMemberProfilesApi} from './people-member-profiles-api';
import {createPeopleDevicesApi} from './people-devices-api';
import {SignInSettingsSource} from './signin-source';
import {LocalOwnerTransferSource} from './local-owner-transfer';
import {MemberResetCodeSource} from './member-reset-code';
import {guardedSettingsRequest, localOwnerTransferTransport} from './settings-request';
import {SettingsStorageSource} from './server/storage-source';
import {StorageDataFolder} from './server/StorageUsage';
import {SettingsOwnerSwitch} from './owner-switch';
import {OwnerSignInSwitch} from './OwnerSignInSwitch';
import {LocalMoveSwitch} from './LocalMoveSwitch';
import {MoveCodesHost} from './MoveCodesHost';
import {MoveInProgress, SignInMoveWatch} from './MoveInProgress';
import {MoveCodesPortProvider, MoveCodeRowAction} from './move-codes-port';
import {ChangeOwner} from './ChangeOwner';
import {LocalChangeOwnerRow} from './LocalChangeOwnerRow';
import {DeviceRowControl} from './device-rows';
import {RemoteNowPlaying} from './RemoteNowPlaying';
import {deviceRowState, isDeviceSyntheticRow} from './device-rows-model';
import {AudioSubtitleLanguageList, SubtitlePreview, isSubtitlePreview, preferredLanguageKind} from './audio-subtitles-rows';
import {subtitlePreviewPaint} from './audio-subtitles-model';
import {MusicEqualizerRow, musicEqualizerAvailable} from './account-music/MusicEqualizerRow';
import {RemoteSourcesPage} from './remote-sources/RemoteSourcesPage';
import {RemoteFormSource} from './server-remote/remote-form-source';
import {CertificateFormSource} from './server-remote/certificate-form-source';
import {CertificateStatusControl, ServerRemotePage} from './server-remote/ServerRemotePage';
import {MetadataSettingsPage} from './metadata/MetadataSettingsPage';
import {browserAccountOrigin} from '../../platform/account-origin';
import {browserAppSessions} from '../../platform/server-connections';
import {SettingsInvitations} from './invitations';
import {LibraryChannelsPage} from './server/channels/LibraryChannelsPage';
import {libraryChannelAdmission} from '../live/owner/library-channel-admission';
import {createOwnerSources, OwnerSourcesPage} from '../live/owner/OwnerSourcesPage';
import {HistoryRetention} from './server-history/HistoryRetention';
import {ServerIntros} from './server-intros/ServerIntros';
import {SegmentIntrosSource} from './server-intros/source';
import {createScreenPlayEffect} from '../browse/playback-effect';
import {ProfileSettingsSource} from './account-profile/source';
import {profileSettingState, changeProfileSetting} from './account-profile/rows';
import {ProfileList} from './account-profile/ProfileList';
import {ProfileDelete} from './account-profile/ProfileDelete';
import {ProfilePicture} from './account-profile/ProfilePicture';
import {ProfileSignOut} from './account-profile/ProfileSignOut';
import {ProfileForgotPIN} from './account-profile/ProfileForgotPIN';
import {ProfileDevicesSource} from './account-profile/devices';
import {ProfileDevices} from './account-profile/ProfileDevices';
import {ProfileOtherSignOut} from './account-profile/ProfileOtherSignOut';
import {ProfileOtherDeviceSignOut} from './account-profile/sign-out-others';
import {ProfileRestrictionsSource} from './account-profile/restrictions';
import {isProfileRestriction, restrictionRow, changeRestriction} from './account-profile/restriction-rows';
import {SettingsStringList} from '../../kit/settings/SettingsStringList';
import {observeProfileFailures} from './account-profile/failures';
import {ProfilePINDialog, type ProfilePINRequest} from './account-profile/ProfilePINControl';
import {ProfileForgetDevices} from './account-profile/ProfileForgetDevices';
import {ProfileSignIn, type ProfileSecurityFacts} from './account-profile/ProfileSignIn';
import {profileSecurityTransport} from './account-profile/security-transport';

// A carry contains calendar bounds only. The captured session object owns its lifetime.
const historyContexts = new WeakMap<ShellAssemblyContext, ReportContext>();
/** A confirmed transfer permanently spends this owner Settings context. Only
 * the shell's actual authority reconciliation can supply a replacement. */
const transferredOwnerContexts = new WeakSet<ShellAssemblyContext>();
const noPreferences = () => () => {};
/** Stable snapshot: the Remote sources page owns its reads and never enters the generic section loader. */
const remoteSourcesPageSnapshot = Object.freeze({loading: false});

const sameBuildInputs = (a: readonly unknown[], b: readonly unknown[]) => a.length === b.length && a.every((value, index) => Object.is(value, b[index]));
const settingsBuildFacts = (context?: ShellAssemblyContext) => {
  const identity = context?.facts.identity, viewer = context?.session().viewer;
  return [identity?.primaryProfile, identity?.capabilities?.manageAccount, identity?.capabilities?.manageServer, identity?.capabilities?.managePeople,
    identity?.capabilities?.manageSignIn, identity?.signIn, viewer?.role, viewer?.serverId, viewer?.authority, viewer?.accountId, viewer?.profileId] as const;
};
const settingsLocationRoute = (location: Readonly<{pathname: string; searchStr: string}>) => settingsRoute(location.pathname === '/dashboard'
  ? {scope: 'server', section: 'dashboard'} : Object.fromEntries(new URLSearchParams(location.searchStr)), () => true);
type SettingsBuild = Readonly<{context: ShellAssemblyContext; inputs: readonly unknown[]; current: () => boolean;
  /** The build's own captured transport, so a holder read keeps this same fence. */
  api: ShellAssemblyContext['api'];
  settings: SettingsBindings; dashboard?: DashboardBindings; profiles?: ProfileSettingsSource;
  profilePINCurrent: (request: ProfilePINRequest) => boolean; closeProfilePIN: (request: ProfilePINRequest) => void}>;

export function SettingsEntry() {
  const protectedContext = useProtectedWebScreenContext();
  const location = useRouterState({select: state => state.location});
  const [owned, setOwned] = useState<SettingsBuild>();
  const [profilePIN, setProfilePIN] = useState<ProfilePINRequest>();
  const [failure, setFailure] = useState(false), [retry, setRetry] = useState(0);
  const context = protectedContext?.context;
  useSyncExternalStore(context?.factsSource?.subscribe ?? noPreferences, () => context?.factsSource?.getSnapshot() ?? context?.facts, () => undefined);
  // The same confirmed reader drives calendar lifetime. A zone change retires
  // the old report factory/results; no old rows can acquire the new zone label.
  const preferences = useSyncExternalStore(context?.preferences?.subscribe ?? noPreferences, () => context?.preferences?.getSnapshot('web'), () => undefined);
  const reportTimeZone = preferences && shellLanguage(preferences).region.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
  const raw = Object.fromEntries(new URLSearchParams(location.searchStr));
  const route = settingsLocationRoute(location);
  const facts = settingsBuildFacts(context);
  const [buildPrimary, buildAccount, buildServer, buildPeople, buildSignIn, buildKind, buildRole, buildServerId, buildAuthority, buildAccountId, buildProfileId] = facts;
  const routeScope = route.scope, routeSection = route.section, hasPreferences = !!preferences;
  // Keep the original build dependencies: ordinary preference confirmations and
  // row-only deep links keep their controller, forms and outstanding intents.
  const inputs = [context, retry, route.scope, route.section, !!preferences, reportTimeZone, ...facts] as const;
  const reportCarry = useRef<Readonly<{inputs: readonly unknown[]; report: ReportContext}> | undefined>(undefined);
  const mediaRuntime = protectedContext?.binding.playback?.runtime;
  // Setup samples these committed ports once; the exact scalar owner keys below govern retirement.
  const setupPorts = useRef({preferences, route, facts, inputs, mediaRuntime});
  useLayoutEffect(() => {setupPorts.current = {preferences, route, facts, inputs, mediaRuntime};});
  useLayoutEffect(() => {
    const {preferences, route, facts, inputs, mediaRuntime} = setupPorts.current;
    const reader = context?.preferences, snapshot = preferences;
    if (!context || !reader || !snapshot || !context.isCurrent() || transferredOwnerContexts.has(context)) return;
    let active = true;
    let inputActive = false,inputRevision = 0;
    const inputListeners = new Set<() => void>();
    const inputActivity = {active:()=>inputActive,revision:()=>inputRevision,subscribe:(listener:()=>void)=>{inputListeners.add(listener);return()=>{inputListeners.delete(listener);};}};
    const lifetime = new AbortController(), authorityListeners = new Set<() => void>();
    let preferencesAvailable = true, confirmedZone = reportTimeZone;
    // Confirmed preference changes narrow this same build immediately. The
    // controller still owns its form intents; only presence/zone retire it.
    const offPreferenceFence = reader.subscribe(() => {
      const confirmed = reader.getSnapshot('web');
      preferencesAvailable = !!confirmed;
      confirmedZone = confirmed && shellLanguage(confirmed).region.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
    });
    const current = () => {
      if (!active || transferredOwnerContexts.has(context) || !context.isCurrent() || !sameBuildInputs(facts, settingsBuildFacts(context))) return false;
      const location = router.state.location;
      if (location.pathname !== '/settings' && location.pathname !== '/dashboard') return false;
      const selected = settingsLocationRoute(location);
      return selected.scope === route.scope && selected.section === route.section && preferencesAvailable && confirmedZone === reportTimeZone;
    };
    const assert = () => {if (!current()) throw new DOMException('Settings build retired', 'AbortError');};
    const signal = (input?: AbortSignal | null) => input ? AbortSignal.any([lifetime.signal, input]) : lifetime.signal;
    const originalApi = context.api;
    const request = guardedSettingsRequest(originalApi, assert, signal);
    const requestRaw: typeof originalApi.requestRaw = async (path, method, options) => {
      assert();const value = await originalApi.requestRaw(path, method, {...options, signal: signal(options?.signal)});assert();return value;
    };
    const requestConsole: typeof originalApi.requestConsole = async <T,>(path: string, method: string, body: unknown, input: AbortSignal): Promise<T> => {
      assert();const value = await originalApi.requestConsole<T>(path, method, body, signal(input));assert();return value;
    };
    const requestCertificate: typeof originalApi.requestCertificate = async <T,>(action: Parameters<typeof originalApi.requestCertificate>[0], body: unknown, input: AbortSignal): Promise<T> => {
      assert();const value = await originalApi.requestCertificate<T>(action, body, signal(input));assert();return value;
    };
    const requestMetadataUpload: typeof originalApi.requestMetadataUpload = async <T,>(path: string, body: FormData, input?: AbortSignal): Promise<T> => {
      assert();const value = await originalApi.requestMetadataUpload<T>(path, body, signal(input));assert();return value;
    };
    const requestBounded: typeof originalApi.requestBounded = async <T,>(path: string, maxBytes: number, input: AbortSignal): Promise<T> => {
      assert();const value = await originalApi.requestBounded<T>(path, maxBytes, signal(input));assert();return value;
    };
    const requestLyrics: typeof originalApi.requestLyrics = async <T,>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> => {
      assert();const value = await originalApi.requestLyrics<T>(path, method, body, signal(input));assert();return value;
    };
    const routeFetch: typeof originalApi.routeFetch = async (input, options) => {
      assert();const value = await originalApi.routeFetch(input, {...options, signal: signal(options?.signal ?? (input instanceof Request ? input.signal : undefined))});assert();return value;
    };
    // Fence Settings transport entry points explicitly while preserving their
    // original method owner and authentication behavior. Binding a method alone
    // does not fence any internal transport calls it might make.
    const methods = new Map<PropertyKey, Readonly<{original: unknown; bound: unknown}>>();
    const api = new Proxy(originalApi, {get(target, property) {
      if (property === 'request') return request;
      if (property === 'requestRaw') return requestRaw;
      if (property === 'requestConsole') return requestConsole;
      if (property === 'requestCertificate') return requestCertificate;
      if (property === 'requestMetadataUpload') return requestMetadataUpload;
      if (property === 'requestBounded') return requestBounded;
      if (property === 'requestLyrics') return requestLyrics;
      if (property === 'routeFetch') return routeFetch;
      const value = Reflect.get(target, property, target);
      if (typeof value !== 'function') return value;
      const held = methods.get(property);
      if (held && held.original === value) return held.bound;
      const bound = value.bind(target);methods.set(property, {original: value, bound});return bound;
    }});
    const buildContext = forwardFacts(context, {api, isCurrent: current});
    const reportError = () => {if (current()) setFailure(true);};
    const language = snapshot ? shellLanguage(snapshot) : defaultI18n;
    const uiLocale = language.locale, appLocale = language.region.formatLocale;
    const identity = context.facts.identity, viewer = context.session().viewer;
    const role = viewer.role === 'owner' || viewer.role === 'admin' ? viewer.role : 'member';
    const metadataCurrent = () => current() && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true;
    const openHome = () => {if (current()) void router.navigate({to: '/'});};
    const openSettings = (section: string) => {if (current()) void router.navigate({to: '/settings', search: () => ({scope: 'server', section})});};
    // A drawn link names the page that owns its value, so it opens that row too.
    const openSettingsRow = (target: {scope: 'account' | 'server'; section: string; row?: string}) => {if (current()) void router.navigate({to: '/settings', search: () => target});};
    const profiles = route.scope === 'account' && route.section === 'profile' && viewer.authority === 'local'
      ? new ProfileSettingsSource(api, {serverId: viewer.serverId, accountId: viewer.accountId, profileId: viewer.profileId}, current, async (path, body, contentType, signal) => {
        if (!current()) throw new Error('shell_authority_retired');
        const response = await api.routeFetch(api.baseUrl + path, {method: 'POST', signal, headers: {Authorization: 'Bearer ' + context.session().accessToken, 'Content-Type': contentType}, body: body as BodyInit});
        if (!response.ok) throw Object.assign(new Error('Profile picture could not be saved.'), {status: response.status});
        return response.json();
      }, buildContext.observeDirect) : undefined;
    const restrictions = profiles ? new ProfileRestrictionsSource(api, profiles, current) : undefined;
    const offRestrictions = restrictions?.subscribe(() => {if (current() && restrictions.getSnapshot().error) reportError();});
    const profileDevices = profiles && identity.signIn === 'local' ? new ProfileDevicesSource(api, profiles, current) : undefined;
    const otherDeviceSignOut = profiles ? new ProfileOtherDeviceSignOut(api, profiles, current) : undefined;
    const offProfileFailures = profiles ? observeProfileFailures(profiles, current, reportError) : undefined;
    const profileSecurityCurrent = () => current() && route.scope === 'account' && route.section === 'profile' &&
      identity.primaryProfile === true && identity.capabilities?.manageAccount === true && viewer.authority === 'local' &&
      profiles?.getSnapshot().document?.canManage === true;
    const profileSecurityRows = (id: string) => ['profile.email','profile.password','profile.twoStep','profile.recoveryCodes'].includes(id);
    const securityCommands = profiles ? profileSecurityTransport(originalApi, () => {if(!profileSecurityCurrent())throw new DOMException('Account security retired','AbortError');}, signal) : undefined;
    let securityFacts: ProfileSecurityFacts = {};
    const securityFactsChanged = (facts: ProfileSecurityFacts) => {if(profileSecurityCurrent())securityFacts=facts;};
    const profileRow = (id: string) => !!profiles && ['profile.list', 'profile.name', 'profile.color', 'profile.username', 'profile.pin', 'profile.pinChange', 'profile.pinForget', 'profile.delete', 'profile.picture'].includes(id);
    let pinRequest: ProfilePINRequest | undefined;
    const profilePINCurrent = (request: ProfilePINRequest) => current() && pinRequest === request;
    const closeProfilePIN = (request: ProfilePINRequest) => {if (pinRequest === request) {pinRequest = undefined;setProfilePIN(held => held === request ? undefined : held);}};
    const openProfilePIN = (remove: boolean) => {
      const target = profiles?.selected(), document = profiles?.getSnapshot().document;
      if (current() && profiles && target && document?.canManage && document.capabilities.manageAccount) {
        pinRequest = {source: profiles, target, remove, trigger: globalThis.document.activeElement as HTMLElement | null};
        setProfilePIN(pinRequest);
      }
    };
    const remoteForm = route.scope === 'server' && route.section === 'remote' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new RemoteFormSource(api, current, true) : undefined;
    const certificate = remoteForm && identity.signIn === 'portico' ? new CertificateFormSource(api, current, reportError) : undefined;
    const platform = settingsPlatform();
    const privacy = createPrivacySettingsSurfaces({api: api, serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId]), isCurrent: current, reportError});
    // One row's engine. A preset row is answered by the rule below instead, so its
    // own missing binding never decides for the rows it sets.
    const engine = (row: Parameters<SettingsController['eligible']>[0]) => {
      if (privacy.eligible(row) || remoteForm?.has(row.id) || certificate?.has(row.id)) return true;
      if (row.id === 'music.eq') return musicEqualizerAvailable(platform);
      const binding = settingsBinding(row.id);
      if (!binding) return row.id === 'device.name' && !!platform?.device?.getSnapshot().facts.name || row.id === 'device.appVersion' && !!platform?.device?.getSnapshot().facts.appVersion;
      if (binding.form === 'preferences') return hasWebPreferenceEffect(row);
      return binding.form !== 'library' && binding.form !== 'remote' && binding.form !== 'managedRoute';
    };
    const options: SettingsControllerOptions = {api: api, serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId]),
      role, mainProfile: identity.primaryProfile === true,
      capabilities: {manageServer: identity.capabilities?.manageServer === true, managePeople: identity.capabilities?.managePeople === true, manageSignIn: identity.capabilities?.manageSignIn === true},
      preferences: snapshot, reader, isCurrent: current, reportError, reading: readingSettingsAdapter,
      environment: {...(platform?.environment ?? {platform: 'web' as const, os: 'browser' as const}), role, signIn: identity.signIn}, device: platform?.device,
      eligibility: {library: () => undefined, hasEngine: row => privacy.eligible(row) ? true : presetEnginesAvailable(row, engine) ?? engine(row)},
    };
    const controller = new SettingsController(options);
    const libraryScope = Object.freeze({serverId:viewer.serverId,viewerId:options.viewerId});
    const session = context.session(), transferOrigin = originalApi.baseUrl, transferRoute = originalApi.getRouteConnection();
    const transferPrincipal = Object.freeze({...viewer, family: session.sessionFamilyId});
    // Independent of this Settings build or dialog. After a known write, only
    // this original shell identity/family may reconcile its actual authority.
    const originalTransferAuthority = () => {
      try {
        if (!context.isCurrent() || !transferRoute || transferRoute.getSnapshot().phase !== 'ready' ||
          originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute ||
          context.facts.identity.signIn !== 'local') return false;
        const now = context.session();
        return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === transferPrincipal.serverId &&
          now.viewer.authority === transferPrincipal.authority && now.viewer.accountId === transferPrincipal.accountId && now.viewer.profileId === transferPrincipal.profileId;
      } catch {return false;}
    };
    let localOwnerTransfer: LocalOwnerTransferSource | undefined;
    const ownerSwitch = identity.primaryProfile === true && role === 'owner' && identity.capabilities?.manageSignIn === true &&
      (identity.signIn === 'local' || identity.signIn === 'portico') ? new SettingsOwnerSwitch(api, {serverId: viewer.serverId,
        isCurrent: () => current() && !localOwnerTransfer?.inFlight && context.session().viewer.role === 'owner' &&
          context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageSignIn === true && context.facts.identity.signIn === identity.signIn,
        hostedOrigin: browserAccountOrigin(import.meta.env, globalThis.location),
        selectionRevision: async () => (await browserAppSessions().read()).revision,
      }) : undefined;
    const localOwnerCurrent = () => {
      if (!current() || route.scope !== 'server' || route.section !== 'signin' || identity.signIn !== 'local' || viewer.authority !== 'local' ||
        !transferRoute || transferRoute.pin.serverId !== transferPrincipal.serverId || transferRoute.getSnapshot().phase !== 'ready' ||
        originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute ||
        ownerSwitch?.getMove() || ownerSwitch?.getSnapshot().busy) return false;
      const now = context.session(), facts = context.facts.identity;
      return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === transferPrincipal.serverId && now.viewer.authority === transferPrincipal.authority &&
        now.viewer.accountId === transferPrincipal.accountId && now.viewer.profileId === transferPrincipal.profileId && now.viewer.role === 'owner' &&
        facts.primaryProfile === true && facts.signIn === 'local' && facts.capabilities?.manageAccount === true &&
        facts.capabilities?.managePeople === true && facts.capabilities?.manageSignIn === true;
    };
    localOwnerTransfer = localOwnerCurrent() ? new LocalOwnerTransferSource(api, {
      accountId: viewer.accountId, profileId: viewer.profileId, isCurrent: localOwnerCurrent,
      transferApi: localOwnerTransferTransport(originalApi, () => {if (!localOwnerCurrent()) throw new DOMException('Settings build retired', 'AbortError');}, signal),
      onTransferred: async publish => {
        // `publish` was checked against the exact opening at acknowledgement;
        // capture that location without another authority call before retirement.
        const location = publish ? router.state.location : undefined;
        // A confirmed write spends this exact owner Settings build immediately.
        // This is unconditional: an ended view cannot revive the old context.
        transferredOwnerContexts.add(context);
        active = false; lifetime.abort();
        for (const listener of authorityListeners) {try {listener();} catch { /* Retire every captured command. */ }}
        authorityListeners.clear();
        setOwned(held => held?.current === current ? undefined : held);
        if (!originalTransferAuthority()) return;
        // No latest context, auth object or location may be adopted here.
        const reconcile = context.auth.permissionDenied();
        if (location && router.state.location === location) void router.navigate({to: '/', replace: true}).catch(() => {});
        await reconcile;
      },
    }) : undefined;
    const signIn = identity.primaryProfile === true && role === 'owner' && identity.capabilities?.manageSignIn === true &&
      (identity.signIn === 'local' || identity.signIn === 'portico') ? new SignInSettingsSource(api, {
        serverId: viewer.serverId, signIn: identity.signIn, isCurrent: current,
        localOwnerTransfer,
        capabilities: () => ({manageAccount: identity.capabilities?.manageAccount === true, managePeople: identity.capabilities?.managePeople === true,
          manageServer: identity.capabilities?.manageServer === true, manageSignIn: current() && identity.capabilities?.manageSignIn === true}),
        onAuthorityChanged: () => {if (current()) void router.navigate({to: '/sign-in', replace: true});},
        ownerSwitch,
      }) : undefined;
    const memberResetPin = transferRoute?.pin.publicKey;
    const memberResetCurrent = () => {
      if (!current() || route.scope !== 'server' || route.section !== 'people' || identity.signIn !== 'local' || viewer.authority !== 'local' ||
        originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute ||
        !transferRoute || !memberResetPin || transferRoute.pin.publicKey !== memberResetPin ||
        transferRoute.pin.serverId !== viewer.serverId || transferRoute.getSnapshot().phase !== 'ready') return false;
      const now = context.session(), facts = context.facts.identity;
      return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === viewer.serverId && now.viewer.authority === viewer.authority &&
        now.viewer.accountId === viewer.accountId && now.viewer.profileId === viewer.profileId && now.viewer.role === 'owner' &&
        facts.primaryProfile === true && facts.signIn === 'local' && facts.capabilities?.managePeople === true && facts.capabilities?.manageAccount === true;
    };
    const memberResetCodes = memberResetCurrent() ? new MemberResetCodeSource({request: async <T,>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> => {
      if (!memberResetCurrent()) throw new DOMException('Settings reset authority retired', 'AbortError');
      const value = await api.request<T>(path, method, body, input);
      if (!memberResetCurrent()) throw new DOMException('Settings reset authority retired', 'AbortError');
      return value;
    }}, {
        serverId: viewer.serverId, accountId: viewer.accountId, isCurrent: memberResetCurrent,
        copy: async code => {
          assert();
          if (!globalThis.navigator?.clipboard?.writeText) throw new Error('Clipboard unavailable.');
          await navigator.clipboard.writeText(code);
        },
      }) : undefined;
    const people: PeopleBindings | undefined = route.section === 'people' && identity.primaryProfile === true && identity.capabilities?.managePeople === true &&
      (role === 'owner' || role === 'admin') && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
        inputActivity, client: new SettingsPeopleClient(api, current), devices: createPeopleDevicesApi(api, current), memberProfiles: createPeopleMemberProfilesApi(api, current, async (path, body, contentType) => {
          assert();
          const response = await api.routeFetch(api.baseUrl + path, {method: 'POST', redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer',
            headers: {Authorization: 'Bearer ' + context.session().accessToken, 'Content-Type': contentType}, body: body as BodyInit});
          if (!response.ok) throw Object.assign(new Error('Profile picture could not be saved.'), {status: response.status});
          return response.json();
        }, context.artwork), get serverName() {return buildContext.facts.serverName;}, get libraries() {return buildContext.facts.identity.libraries;}, subscribe: listener => subscribeFacts(buildContext, listener), memberResetCodes,
        actor: {accountId: viewer.accountId, role}, streamLimits: peopleStreamLimits,
        invitations: new SettingsInvitations(api, current, api.baseUrl, viewer.serverId), signIn: identity.signIn, openHome, reportError,
        // The owner's invite expiry, in whole days on the server and hours in the
        // dialog, read live so a save is used by the next invitation only.
        inviteRole: () => {
          const stored = controller.rowState(settingsRowIndex.get('people.inviteRole')!.row).value;
          return stored === 'administrator' ? 'admin' : stored === 'member' ? 'member' : undefined;
        },
        inviteExpiryHours: () => {
          const state = controller.rowState(settingsRowIndex.get('people.inviteExpiry')!.row);
          const days = Number(state.value);
          return Number.isFinite(days) && days > 0 ? days * 24 : undefined;
        },
        authority: {scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: session.sessionFamilyId},
          isCurrent: current, subscribe: listener => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};}, onAuthorityChanged: openHome},
      } : undefined;
    let dashboard: DashboardBindings | undefined;
    if (identity.primaryProfile === true && identity.capabilities?.manageServer && (role === 'owner' || role === 'admin')) {
      const pending = route.section === 'history' ? historyContexts.get(context) : undefined;
      if (pending) historyContexts.delete(context);
      const carryInputs = [context, route.scope, route.section, !!snapshot, reportTimeZone, ...facts];
      const held = reportCarry.current;
      const carried = pending?.timeZone === reportTimeZone ? pending : held && sameBuildInputs(held.inputs, carryInputs) ? held.report : undefined;
      const report = carried ?? captureReportContext(reportTimeZone);
      reportCarry.current = {inputs: carryInputs, report};
      dashboard = createDashboardBindings({api: api, events: context.events, scope: {serverId: viewer.serverId, viewerId: options.viewerId},
        context: report, role, isCurrent: current, isMainProfile: () => current() && context.facts.identity.primaryProfile === true,
        artwork: (id, kind, revision) => revision ? context.artwork.foreground(encodedRoutePath('get_items_id_art_kind', {id: encodeURIComponent(id), kind}) + '?' + new URLSearchParams({v: revision}), kind === 'backdrop' ? 800 : 400, kind === 'poster' ? 2 / 3 : kind === 'backdrop' ? 16 / 9 : 1) : undefined,
        effects: {openHome, openSettings, reportError, openHistory: report => {if (current()) {historyContexts.set(context, report);openSettings('history');}}}});
    }
    // Server › Streaming › Hardware draws measured adapters, formats and probes:
    // one owner read per visit, and one deliberate re-probe from its own button.
    const hardware = route.section === 'streaming' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new SettingsHardwareSource(api, () => current(), reportError) : undefined;
    if (hardware) void hardware.load();
    // Server › General › Updates: the cached report the page reads, and one
    // explicit feed check only when the owner presses Check now.
    const updates = route.section === 'general' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new SettingsUpdatesSource(api, () => current(), reportError) : undefined;
    if (updates) void updates.load();
    // Server › General › Tools draws the two owner actions the page owns: the
    // restart confirmation and the notification composer, both over their real
    // owner routes (no section read, no invented state).
    const generalActions = route.section === 'general' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true;
    // Location is its own runtime preference, independent of the disk folder.
    const storedLocation = (): string | undefined => {
      const location = settingsRowIndex.get('streaming.transcodeLocation');
      if (!location) return undefined;
      const state = controller.rowState(location.row);
      return typeof state.value === 'string' ? state.value : undefined;
    };
    // The owner's Libraries page owns its own bounded directory read and scan
    // commands; the settings controller keeps the per-library panel only.
    const libraries = route.section === 'libraries' && identity.primaryProfile === true && identity.capabilities?.manageServer === true && (role === 'owner' || role === 'admin')
      ? new LibraryManagementService({api: api, scope: libraryScope}) : undefined;
    // Storage & backups owns one bounded usage sweep shared by its drawn rows.
    const server = route.section === 'storage' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true ? createServerSettingsSurfaces({transport: api, serverId: viewer.serverId, isCurrent: current, openTarget: openSettingsRow,
      backups: controller.backups, subscribe: controller.subscribe, scopeKey: `${viewer.serverId}/${options.viewerId}`, reportError}) : undefined;
    const troubleshooting = route.section === 'troubleshooting' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true ? createTroubleshootingSurfaces({transport: api, serverId: viewer.serverId,
      viewerId: options.viewerId, downloadBase: api.baseUrl, isCurrent: current, subscribe: controller.subscribe, reportError,
      messageConnection: {open: (headers, signal) => {
        if (!current()) return Promise.reject(new DOMException('The settings scope ended.', 'AbortError'));
        return api.routeFetch(api.baseUrl + '/v1/admin/logs/events', {headers: {...headers, Authorization: 'Bearer ' + context.session().accessToken}, signal});
      }}}) : undefined;
    const playbackProblems = troubleshooting ? new PlaybackProblemsSource({transport:api,isCurrent:current,reportError,subscribe:listener=>{authorityListeners.add(listener);return()=>{authorityListeners.delete(listener);};}}) : undefined;
    const previewLife = new AbortController();
    const preview = createScreenPlayEffect({isCurrent: current, runtime: () => mediaRuntime});
    // The Sign-in page's Reset file group names the server's own data folder. It is
    // the same bounded storage report the Storage page reads, only when the owner
    // may manage this server.
    const signinStorage = route.section === 'signin' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new SettingsStorageSource(api, viewer.serverId, current, reportError) : undefined;
    const intros = route.section === 'segments' && identity.primaryProfile === true && identity.capabilities?.manageServer === true && (role === 'owner' || role === 'admin')
      ? new SegmentIntrosSource({api: api, isCurrent: current, reportError}) : undefined;
    const live = route.section === 'live' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? createOwnerSources({api: api, serverId: viewer.serverId, viewerId: options.viewerId,
        isCurrent: () => current() && context.session().viewer.role === 'owner' && context.session().viewer.serverId === viewer.serverId && context.session().viewer.authority === viewer.authority && context.session().viewer.accountId === viewer.accountId && context.session().viewer.profileId === viewer.profileId && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true,
        reportError}) : undefined;
    const integrationKeys = route.section === 'integrations' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new SettingsApiKeysSource(api, current, reportError) : undefined;
    const webhooks = integrationKeys ? new SettingsWebhooksSource(api, current, reportError) : undefined;
    const missing = route.section === 'libraries' && role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true
      ? new SettingsMissingSource(api, current, reportError) : undefined;
    const channelPorts = live ? libraryChannelAdmission(buildContext, live) : undefined;
    const source = createSettingsBindings(controller, options, identity, {openHome, signIn,
      special: (row, state, rowContext, openPanel) => {
        if (row.id === 'profile.devices' && profileDevices) return <ProfileDevices source={profileDevices} isCurrent={current} onFailure={reportError} signOut={context.signOut} />;
        if (row.id === 'profile.pinForgot' && profiles && identity.signIn === 'local') return <ProfileForgotPIN source={profiles} api={api} selectedFamilyId={() => context.session().sessionFamilyId} isCurrent={current} />;
        if (row.id === 'profile.signOutElsewhere' && otherDeviceSignOut) return <ProfileOtherSignOut command={otherDeviceSignOut} isCurrent={current} disabled={state.disabled} onFailure={reportError} />;
        if (row.id === 'profile.signOut') return <ProfileSignOut serverName={buildContext.facts.identity.serverName} isCurrent={current} signOut={context.signOut} />;
        if (row.id === 'profile.hiddenLabels' && restrictions) return <SettingsStringList label={defaultI18n.t(row.label)} values={restrictions.getSnapshot().document?.blockedLabels ?? []} addLabel={defaultI18n.t(row.control.addLabel!)} disabled={state.disabled} onChange={value => {void changeRestriction(restrictions, row.id, value)?.catch(reportError);}} />;
        if (row.id === 'remote.homeNets') return <SettingsStringList label={defaultI18n.t(row.label)} values={Array.isArray(state.value) ? state.value as string[] : []} addLabel={defaultI18n.t(row.control.addLabel!)} disabled={state.disabled} onChange={value => controller.change(row, value, rowContext)} />;
        if (row.id === 'profile.picture' && profiles) return <ProfilePicture source={profiles} isCurrent={current} onFailure={reportError} />;
        if (row.id === 'profile.delete' && profiles) return <ProfileDelete source={profiles} isCurrent={current} onFailure={reportError} />;
        if (row.id === 'signin.people') return <Button size="small" variant="glass" disabled={state.disabled} onClick={() => {if (current()) openSettingsRow({scope: 'server', section: 'people'});}}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
        if (row.id === 'signin.outside') return <Button size="small" variant="glass" disabled={state.disabled} onClick={() => {if (current()) openSettingsRow({scope: 'server', section: 'remote'});}}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
        if (row.id === 'signin.dataFolder' && signinStorage) return <StorageDataFolder source={signinStorage} label={defaultI18n.t(row.label)} />;
        if (row.id === 'general.version' && updates) return <UpdatesCurrent source={updates} />;
        if (row.id === 'general.checkNow' && updates) return <UpdatesCheckNow source={updates} label={defaultI18n.t(row.control.label ?? row.label)} disabled={state.disabled === true} />;
        if ((row.id === 'general.message' || row.id === 'general.restart') && generalActions) {
          const label = defaultI18n.t(row.control.label ?? row.label);
          return row.id === 'general.message'
            ? <GeneralMessage api={api} isCurrent={current} reportError={reportError} disabled={state.disabled === true} label={label} />
            : <GeneralRestart api={api} isCurrent={current} reportError={reportError} disabled={state.disabled === true} label={label}
                confirm={row.control.confirm ? defaultI18n.t(row.control.confirm) : undefined} />;
        }
        if (['signin.switchToPortico', 'signin.startOverPortico'].includes(row.id) && signIn?.ownerSwitch) return <OwnerSignInSwitch source={signIn.ownerSwitch} startOver={row.id === 'signin.startOverPortico'} isCurrent={current} onError={reportError}/>;
        if (['signin.switchToLocal', 'signin.startOverLocal'].includes(row.id) && signIn?.ownerSwitch) return <LocalMoveSwitch source={signIn.ownerSwitch} startOver={row.id === 'signin.startOverLocal'} isCurrent={current} onError={reportError}/>;
        if (row.id === 'signin.changeOwner' && signIn?.ownerSwitch) return <ChangeOwner source={signIn.ownerSwitch} isCurrent={current} onError={reportError}/>;
        if (row.id === 'signin.changeOwnerLocal' && signIn?.localOwnerTransfer) return <LocalChangeOwnerRow source={signIn.localOwnerTransfer} isCurrent={current} onError={reportError}/>;
        if (row.id === 'profile.pinForget' && profiles) return <ProfileForgetDevices source={profiles} isCurrent={current} onFailure={reportError} />;
        if (row.id === 'profile.pinChange' && profiles) return <Button size="small" variant="glass" disabled={state.disabled} onClick={() => openProfilePIN(false)}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
        if (row.id === 'profile.list' && profiles) return <ProfileList source={profiles} isCurrent={current} onFailure={reportError} onSelect={() => {
          requestAnimationFrame(() => {if (current()) document.getElementById('profile.name')?.querySelector<HTMLInputElement>('input')?.focus();});
        }} />;
        if (row.id === 'streaming.transcodeFolder' && hardware) return <TranscodeFolder label={defaultI18n.t(row.label)} value={typeof state.value === 'string' ? state.value : ''}
          disabled={state.disabled} api={api} onChange={path => {if (current()) controller.change(settingsRowIndex.get('streaming.transcodeFolder')!.row, path, rowContext);}} />;
        if (row.id === 'streaming.hwDevices' && hardware) return <HardwareDevices source={hardware} />;
        if (row.id === 'streaming.hwTest' && hardware) return <TestHardware source={hardware} />;
        if (row.id === 'storage.missingNow' && missing) return <MissingTitles source={missing} isCurrent={current} artwork={id => context.artwork.foreground(encodedRoutePath('get_items_id_art_kind', {id: encodeURIComponent(id), kind: 'poster'}), 80, 2 / 3)} />;
        const storageControl = server?.special(row, state);
        if (storageControl !== undefined) return storageControl;
        const privacyControl = privacy.special(row, state);
        if (privacyControl !== undefined) return privacyControl;
        const languageKind = preferredLanguageKind(row.id);
        if (languageKind) return <AudioSubtitleLanguageList row={row} state={state} locale={uiLocale} appLocale={appLocale}
          onChange={value => {if (current()) controller.change(row, value, rowContext);}} onError={reportError} />;
        if (isSubtitlePreview(row.id)) {
          const value = (id: string) => controller.rowState(settingsRowIndex.get(id)!.row, rowContext).value;
          return <SubtitlePreview paint={subtitlePreviewPaint({size: value('audioSubtitles.subtitleSize'), color: value('audioSubtitles.subtitleColor'),
            background: value('audioSubtitles.subtitleBackground'), position: value('audioSubtitles.subtitlePosition')})}
            text={defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.2.rows.0.control.text')} />;
        }
        if (row.id === 'device.remote') return <RemoteNowPlaying api={api} isCurrent={current} onFailure={reportError}/>;
        if (isDeviceSyntheticRow(row.id)) return <DeviceRowControl key={context.epoch} row={row} api={api} isCurrent={current} onSwitchServer={() => {if (current()) void router.navigate({to: '/servers'});}}/>;
        if (row.id === 'music.eq') return <MusicEqualizerRow platform={platform} isCurrent={current}/>;
        if (row.id === 'appearance.customizeHome') return <CustomizeHomeRow api={api} home={context.home} serverId={viewer.serverId} viewerId={options.viewerId} isCurrent={current}/>;
        if (row.id === 'libraries.name' && isLibraryPanelContext(rowContext) && rowContext.recordId && typeof state.value === 'string') {
          const libraryId = rowContext.recordId;
          return <RenameLibrary key={libraryId} name={state.value} disabled={state.disabled === true || state.saving === true} rename={name => controller.renameLibrary(libraryId, name)} />;
        }
        if (row.id === 'libraries.list' && libraries) return <LibrariesPanel api={api} service={libraries} serverName={buildContext.facts.identity.serverName}
          onEdit={libraryId => { if (current()) openPanel('library', libraryId); }} onError={reportError} />;
        if (row.id === 'libraries.skipped' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries)
          return <SkippedFiles libraryId={rowContext.recordId} api={options.api} service={libraries} isCurrent={current} label={defaultI18n.t(row.control.label ?? row.label)} />;
        if (row.id === 'libraries.folders' && isLibraryPanelContext(rowContext) && rowContext.recordId) {
          return <LibraryFolders libraryId={rowContext.recordId} api={options.api} serverName={buildContext.facts.identity.serverName}
            scope={libraryScope} isCurrent={current} onError={reportError} />;
        }
        if (row.id === 'libraries.remove' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries && role === 'owner')
          return <RemoveLibraryButton key={rowContext.recordId} libraryId={rowContext.recordId} service={libraries} api={api}
            label={defaultI18n.t(row.control.label ?? row.label)} onRemoved={() => {if (current()) {openPanel('');void libraries.loadLibraries().catch(reportError);}}} onError={reportError}/>;
        if (row.id === 'libraries.unmatched' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries) {
          const libraryId = rowContext.recordId;
          return <Button variant="glass" size="small" disabled={state.disabled === true} onClick={() => {
            if (current()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {unmatched: true}}).catch(reportError);
          }}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
        }
        if (row.id === 'libraries.fetchLyrics' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries && role === 'owner')
          return <LibraryLyricsAction key={rowContext.recordId} api={api} libraryId={rowContext.recordId} isCurrent={current}
            disabled={state.disabled === true || state.saving === true || controller.rowState(settingsRowIndex.get('libraries.lyrics')!.row, rowContext).value !== 'lrclib'}
            label={defaultI18n.t(row.control.label ?? row.label)} onError={reportError}/>;
        if (row.control.type === 'action' && libraryScanAction(row.id) && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries)
          return <LibraryScanAction rowId={row.id} label={defaultI18n.t(row.control.label ?? row.label)} libraryId={rowContext.recordId} service={libraries} onError={reportError} />;
        return row.id === 'metadata.perLibrary' ? <Button variant="glass" size="small" onClick={() => openSettings('libraries')}>{defaultI18n.t(row.control.label ?? row.label)}</Button> : null;
      },
      section: (selected, _openPanel, renderRows) => selected.section.scope === 'account' && selected.section.id === 'profile' && profiles && securityCommands
        ? <>{selected.section.groups?.map((group,index) => group.rows.some(row => row.id === 'profile.password')
          ? <ProfileSignIn key={index} session={context.session} profiles={profiles} api={api} commandApi={securityCommands} signIn={identity.signIn === 'portico' ? 'portico' : 'local'} isCurrent={profileSecurityCurrent} onEnded={context.signOut} onFacts={securityFactsChanged}/>
          : <Fragment key={index}>{renderRows({groups:[group]})}</Fragment>)}{selected.section.advanced ? renderRows({advanced:selected.section.advanced}) : null}</>
        : selected.section.scope === 'server' && selected.section.id === 'signin'
        ? <SignInMoveWatch source={signIn?.ownerSwitch}>{move => <>
            {move && signIn?.ownerSwitch ? <MoveInProgress move={move} source={signIn.ownerSwitch} isCurrent={current}
              onPeople={() => {if (current()) openSettingsRow({scope: 'server', section: 'people'});}} onError={reportError} /> : null}
            {renderRows(selected.section)}
          </>}</SignInMoveWatch>
        : selected.section.scope === 'server' && selected.section.id === 'remote'
        ? <ServerRemotePage api={api} isCurrent={current} source={remoteForm} certificate={certificate} groups={renderRows({groups: selected.section.groups})} advanced={selected.section.advanced ? renderRows({advanced: selected.section.advanced}) : null}/>
        : selected.section.scope === 'server' && selected.section.id === 'metadata' && metadataCurrent() ? <MetadataSettingsPage api={api} isCurrent={metadataCurrent} onLibraries={() => openSettings('libraries')}/> : selected.section.id === 'integrations' && integrationKeys && webhooks ? <IntegrationsPage api={api} source={integrationKeys} webhooks={webhooks} baseUrl={api.baseUrl} isCurrent={current} reportError={reportError}
        otherRows={renderRows({groups: selected.section.groups?.filter(group => !group.rows.some(row => ['integrations.apiKeys', 'integrations.webhooks', 'integrations.assistantAddress', 'integrations.assistantKey'].includes(row.id)))})}
        advancedRows={renderRows({advanced: selected.section.advanced})} /> : selected.section.id === 'live' && live ? <OwnerSourcesPage source={live} libraryChannels={<LibraryChannelsPage ports={channelPorts!}/>}/> : selected.section.id === 'remote-sources' ? <RemoteSourcesPage api={api} serverId={viewer.serverId} viewerId={options.viewerId} isCurrent={current} reportError={reportError} openSection={openSettings} /> : selected.section.id === 'segments' && intros ? <ServerIntros source={intros} onPreview={(libraryId, itemId, startSeconds) => {if (current()) void preview({libraryId, itemId, startSeconds}, previewLife.signal, current).catch(reportError);}} modeRows={renderRows({groups: selected.section.groups?.map(group => ({...group, rows: group.rows.filter(row => row.id === 'segments.mode')})).filter(group => group.rows.length), advanced: selected.section.advanced})} /> : selected.section.id === 'people' && people ? <MoveCodesPortProvider>{signIn?.ownerSwitch && <MoveCodesHost source={signIn.ownerSwitch} serverName={buildContext.facts.identity.serverName} isCurrent={current} onError={reportError}/>}<SignInMoveWatch source={signIn?.ownerSwitch}>{move => <PeopleScreen bindings={people} advancedRows={selected.section.advanced ? renderRows({advanced: selected.section.advanced}) : undefined} initialStatus={move ? 'waiting' : undefined}
              rowActions={person => person.status === 'waiting' ? <MoveCodeRowAction accountId={person.accountId} hasCode={person.hasMoveCode} /> : null} />}</SignInMoveWatch></MoveCodesPortProvider> : selected.section.id === 'dashboard' && dashboard ? <DashboardScreen bindings={dashboard} embedded /> : selected.section.id === 'history' && dashboard ? <><HistoryScreen bindings={dashboard} embedded /><HistoryRetention section={selected.section} renderRows={renderRows}/></> : selected.section.id === 'troubleshooting' ? troubleshooting?.render(<MessageOptions>{renderRows({groups:selected.section.advanced?.groups?.map(group=>({...group,rows:group.rows.filter(row=>['troubleshooting.logLevel','troubleshooting.detailLength','troubleshooting.keepLogs','troubleshooting.keepLogsByKind'].includes(row.id))})).filter(group=>group.rows.length)})}</MessageOptions>,controller.messageDetailMinutes(),playbackProblems && <div id="troubleshooting.playbackProblems"><PlaybackProblems source={playbackProblems}/></div>,selected.section.advanced?.groups?.flatMap(group=>group.rows).filter(row=>controller.rowState(row).changed).map(row=>defaultI18n.t(row.label))) : undefined,
    }, {identity: () => buildContext.facts.identity, subscribe: listener => subscribeFacts(buildContext, listener), getSnapshot: () => buildContext.facts});
    const deviceFacts = {get serverName() {return buildContext.facts.identity.serverName;}, linkTvAvailable: !!api, get serverVersion() {return buildContext.facts.identity.serverVersion;}};
    const lyricsSourceIsLrclib = (selected: SettingsContext) => {
      const row = settingsRowIndex.get('libraries.lyrics')?.row;
      return !!row && controller.rowState(row, selected).value === 'lrclib';
    };
    const quietInterests: ReturnType<typeof mountQuietInput>[] = [];
    const settings = forwardSettingsBindings(source, {
      setOpen: open => {for(const interest of quietInterests)interest.setOpen(open);},
      inputActive: value => {
        if(!current() || inputActive===value)return;
        inputActive=value;++inputRevision;
        controller.setQuietInputActive(value);profiles?.setQuietInputActive(value);
        if(value){remoteForm?.invalidateQuiet();certificate?.invalidateQuiet();}
        for(const listener of inputListeners)listener();
      },
      panelIntro: selected => selected.recordId && selected.tabId === 'metadata' && libraries ? <MetadataSource key={selected.recordId} libraryId={selected.recordId} api={api} isCurrent={current} /> : null,
      panelHeader: (selected, title) => selected.recordId && libraries ? <LibraryHeader key={selected.recordId} libraryId={selected.recordId} name={title} rename={name => controller.renameLibrary(selected.recordId!, name)} service={libraries} artwork={context.artwork} onError={reportError}
        lyrics={{api: api, available: lyricsSourceIsLrclib(selected), isCurrent: current}} /> : null,
      subscribe: listener => {const off = source.subscribe(listener), offProfiles = profiles?.subscribe(listener), offLimits = restrictions?.subscribe(listener), offHardware = hardware?.subscribe(listener), offServer = server?.subscribe(listener), offRemote = remoteForm?.subscribe(listener), offCertificate = certificate?.subscribe(listener);return () => {off();offProfiles?.();offLimits?.();offHardware?.();offServer?.();offRemote?.();offCertificate?.();};},
      change: (row, value, selected) => {
        if (!current()) return;
        if (remoteForm?.has(row.id)) {remoteForm.change(row.id, value);return;}
        if (certificate?.has(row.id)) {certificate.change(row.id, value);return;}
        if (restrictions && isProfileRestriction(row.id)) {void changeRestriction(restrictions, row.id, value)?.catch(reportError);return;}
        if (profiles && row.id === 'profile.pin' && typeof value === 'boolean') {openProfilePIN(!value);return;}
        if (profiles && profileRow(row.id)) {void changeProfileSetting(profiles, row.id, value)?.catch(reportError);return;}
        source.change(row, value, selected);
      },
      // The Remote sources page owns its own checked source reads; the generic
      // server-section configuration read would replace that page with a failure.
      getSnapshot: section => section.scope === 'server' && section.id === 'remote' && remoteForm ? remoteForm.pageSnapshot(source.getSnapshot(section), certificate?.getSnapshot()) : section.scope === 'account' && section.id === 'profile' && profiles ? profiles.getSnapshot() : section.scope === 'server' && (section.id === 'remote-sources' || section.id === 'metadata' && metadataCurrent() || section.id === 'live' && !!live) ? remoteSourcesPageSnapshot : section.scope === 'server' && section.id === 'storage' && server ? server.pageSnapshot(source.getSnapshot(section)) : section.scope === 'server' && section.id === 'streaming' && hardware ? hardware.pageSnapshot(source.getSnapshot(section)) : source.getSnapshot(section),
      load: section => section.scope === 'server' && section.id === 'remote' && remoteForm ? Promise.all([remoteForm.load(), certificate?.load(), source.load(section)]).then(() => undefined) : section.scope === 'account' && section.id === 'profile' && profiles ? Promise.all([profiles.load(), source.load(section)]).then(() => undefined) : section.scope === 'server' && (section.id === 'remote-sources' || section.id === 'metadata' && metadataCurrent() || section.id === 'live' && !!live) ? Promise.resolve() : source.load(section),
      eligible: (row, selected) => (remoteForm?.has(row.id) || certificate?.has(row.id)) ? !!remoteForm?.current() && selected.section.scope === 'server' && selected.section.id === 'remote' && controller.sectionVisible(selected.section) : row.id === 'libraries.list' ? !!libraries && current() && controller.sectionVisible(selected.section) : profileSecurityRows(row.id) ? profileSecurityCurrent() : hardware && hardwareEligible(row.id, hardware.getSnapshot(), storedLocation()) ? current() && controller.sectionVisible(selected.section) : row.id === 'profile.devices' ? !!profileDevices && current() && selected.section.scope === 'account' && selected.section.id === 'profile' : row.id === 'profile.pinForgot' ? !!profiles && identity.signIn === 'local' && current() : row.id === 'profile.signOutElsewhere' ? !!otherDeviceSignOut && current() && selected.section.scope === 'account' && selected.section.id === 'profile' : row.id === 'profile.signOut' ? current() && selected.section.scope === 'account' && selected.section.id === 'profile' : row.id === 'signin.people' ? current() && (role === 'owner' || role === 'admin') && controller.sectionVisible(selected.section) : row.id === 'signin.outside' ? current() && role === 'owner' && controller.sectionVisible(selected.section) : row.id === 'signin.dataFolder' ? !!signinStorage && current() && controller.sectionVisible(selected.section) : row.id === 'general.message' || row.id === 'general.restart' ? generalActions && current() && controller.sectionVisible(selected.section) : row.id === 'storage.missingNow' ? !!missing && current() : row.id === 'storage.removeMissing' ? false : profileRow(row.id) ? current() && selected.section.scope === 'account' && selected.section.id === 'profile' : server?.eligible(row) ?? ((row.id === 'libraries.folders' || (row.id === 'libraries.skipped' || row.id === 'libraries.unmatched') && !!libraries || (row.id === 'libraries.fetchLyrics' || row.id === 'libraries.remove') && !!libraries && role === 'owner') ? current() && isLibraryPanelContext(selected) && controller.sectionVisible(selected.section) : libraryScanAction(row.id) ? current() && !!libraries && selected.panelId === 'library' && !!selected.recordId && controller.sectionVisible(selected.section) : isSubtitlePreview(row.id) ? current() && controller.sectionVisible(selected.section) : row.id === 'device.remote' ? current() && selected.section.scope === 'account' && selected.section.id === 'device' : isDeviceSyntheticRow(row.id) ? current() && controller.sectionVisible(selected.section) && deviceRowState(row, deviceFacts) !== undefined : row.id === 'music.eq' ? current() && controller.sectionVisible(selected.section) && musicEqualizerAvailable(platform) : row.id === 'people.table' ? !!people && current() && controller.sectionVisible(selected.section) : row.id === 'metadata.perLibrary' || row.id === 'appearance.customizeHome' ? current() && controller.sectionVisible(selected.section) : source.eligible(row, selected)),
      reset: (row, selected) => {if (remoteForm?.has(row.id)) remoteForm.reset(row.id);else if (certificate?.has(row.id)) certificate.reset(row.id);else source.reset(row, selected);},
      rowState: (row, selected) => {
        if (remoteForm?.has(row.id)) return remoteForm.rowState(row);
        if (certificate?.has(row.id)) {const state = certificate.rowState(row);return row.id === 'remote.secureStatus' && state.present ? {...state, control: <CertificateStatusControl source={certificate}/>} : state;}
        if(profileSecurityRows(row.id))return {present: profileSecurityCurrent() && (row.id !== 'profile.recoveryCodes' || securityFacts.mfa === true), value: row.id === 'profile.twoStep' ? securityFacts.mfa : undefined, help: row.id === 'profile.email' ? securityFacts.email : undefined};
        if (row.id === 'storage.missingRule') {
          const state = source.rowState(row, selected), exact = settingsRowIndex.get('storage.missingDaysExact')!.row;
          const days = controller.rowState(exact, selected).value;
          if (!state.present || typeof days !== 'number') return state;
          const live = () => current() && !controller.rowState(row, selected).disabled && controller.rowState(row, selected).value === state.value && controller.rowState(exact, selected).value === days;
          return {...state, control: <MissingRetention key={`${String(state.value)}:${days}`} row={row} state={state} days={days} isCurrent={live} onChange={value => {if (live()) controller.change(row, value, selected);}} />};
        }
        if (row.id === 'profile.devices') return {present: !!profileDevices && current() && profiles?.getSnapshot().document?.canManage === true};
        if (row.id === 'profile.pinForgot') return {present: !!profiles && identity.signIn === 'local' && current() && profiles.getSnapshot().document?.canManage === false && profiles.selected()?.pinRequired === true};
        if (row.id === 'profile.signOutElsewhere') return {present: !!otherDeviceSignOut && current() && profiles?.getSnapshot().document?.canManage === true, disabled: profiles?.getSnapshot().busy};
        if (row.id === 'profile.signOut') return {present: current()};
        // These are domain-backed rows, not configuration bindings. Project the
        // same captured visibility as their existing controls and eligibility.
        if (row.id === 'signin.people') return {present: current() && (role === 'owner' || role === 'admin') && controller.sectionVisible(selected.section)};
        if (row.id === 'signin.outside') return {present: current() && role === 'owner' && controller.sectionVisible(selected.section)};
        if (row.id === 'signin.dataFolder') return {present: !!signinStorage && current() && controller.sectionVisible(selected.section)};
        if (restrictions && profiles && isProfileRestriction(row.id)) return restrictionRow(restrictions, profiles, row.id, role === 'owner')!;
        if (row.id === 'libraries.list') return {present: !!libraries && current() && controller.sectionVisible(selected.section)};
        if (row.id === 'storage.missingNow') return {present: !!missing && current()};
        if (row.id === 'storage.removeMissing') return {present: false};
        if (row.id === 'general.message' || row.id === 'general.restart') return {present: generalActions && current() && controller.sectionVisible(selected.section)};
        if (profiles && profileRow(row.id)) return row.id === 'profile.list' ? {present: current() && !!profiles.getSnapshot().document} : profileSettingState(profiles, row.id)!;
        const privacyState = privacy.rowState(row);
        if (privacyState !== undefined) return privacyState;
        if (row.id === 'libraries.folders' || (row.id === 'libraries.skipped' || row.id === 'libraries.unmatched') && !!libraries || (row.id === 'libraries.fetchLyrics' || row.id === 'libraries.remove') && !!libraries && role === 'owner') return {present: current() && isLibraryPanelContext(selected) && controller.sectionVisible(selected.section)};
        if (libraryScanAction(row.id)) return {present: current() && !!libraries && selected.panelId === 'library' && !!selected.recordId && controller.sectionVisible(selected.section)};
        if (isSubtitlePreview(row.id)) return {present: current() && controller.sectionVisible(selected.section)};
        if (row.id === 'streaming.hardware') {
          // The adapters come from the hardware report; until it has answered,
          // the row offers only the spec's own two choices.
          const state = source.rowState(row, selected), devices = hardware?.getSnapshot().capacity?.hardware.devices ?? [];
          return state.present ? {...state, choices: hardwareSelectionChoices(devices, state.value, defaultI18n.t)} : state;
        }
        const hardwareState = hardware ? hardwareRowState(row.id, hardware.getSnapshot(), hardwareRowIds.includes(row.id) ? storedLocation() : undefined) : undefined;
        if (hardwareState !== undefined) return hardwareState;
        const device = isDeviceSyntheticRow(row.id) ? deviceRowState(row, deviceFacts) : undefined;
        return server?.rowState(row) ?? device ?? (row.id === 'music.eq' ? {present: current() && controller.sectionVisible(selected.section) && musicEqualizerAvailable(platform)} : row.id === 'people.table' ? {present: !!people && current() && controller.sectionVisible(selected.section)} : row.id === 'metadata.perLibrary' || row.id === 'appearance.customizeHome' ? {present: current() && controller.sectionVisible(selected.section)} : source.rowState(row, selected));
      },
    });
    const publishedSettings = forwardSettingsBindings(settings, {
      change: (...args) => {if (current()) settings.change(...args);},
      reset: (...args) => {if (current()) settings.reset(...args);},
      load: (...args) => current() ? settings.load(...args) : Promise.resolve(),
      ...(settings.loadContext ? {loadContext: (...args: Parameters<NonNullable<SettingsBindings['loadContext']>>) => current() ? settings.loadContext!(...args) : Promise.resolve()} : {}),
    });
    const section=settingsSpec.find(section=>section.scope===route.scope && section.id===route.section)!;
    const quietInputs: QuietInput[] = route.scope==='server' && ['signin','live','metadata','remote-sources'].includes(route.section) ? [] : [...controller.quietInputs(section)];
    if(profiles)quietInputs.push({id:'profiles',refresh:profiles.refresh,cancel:profiles.invalidateQuiet,subscribe:profiles.subscribe,
      blocked:()=>profiles.quietBlocked() || profiles.getSnapshot().loading,settlement:()=>profiles.getSnapshot().document});
    if(remoteForm)quietInputs.push({id:'remote',refresh:remoteForm.refresh,cancel:remoteForm.invalidateQuiet,subscribe:remoteForm.subscribe,blocked:remoteForm.quietBlocked,settlement:()=>remoteForm.getSnapshot().draft});
    if(certificate)quietInputs.push({id:'certificate',refresh:certificate.refresh,cancel:certificate.invalidateQuiet,subscribe:certificate.subscribe,blocked:certificate.quietBlocked,settlement:()=>certificate.getSnapshot().draft});
    if(signIn && route.section==='signin')quietInputs.push({id:'signin',refresh:signIn.refresh,cancel:signIn.invalidateQuiet,subscribe:signIn.subscribe,settlement:()=>signIn.getSnapshot().document});
    quietInterests.push(...quietInputs.map(input=>mountQuietInput(context.freshness.hub,`settings/${route.scope}/${route.section}`,[dependsOn.settings('server'),dependsOn.libraries],{
      ...input,blocked:()=>inputActive || !!input.blocked?.(),
      subscribe:listener=>{const off=input.subscribe?.(listener),offInput=inputActivity.subscribe(listener);return()=>{off?.();offInput();};},
    })));
    setFailure(false);setOwned({context, api, inputs, current, settings: publishedSettings, dashboard, profiles, profilePINCurrent, closeProfilePIN});
    return () => {active = false;for(const interest of quietInterests)interest.dispose();inputListeners.clear();offPreferenceFence();pinRequest = undefined;lifetime.abort();setProfilePIN(held => held?.source === profiles ? undefined : held);profileDevices?.dispose();otherDeviceSignOut?.dispose();offRestrictions?.();restrictions?.dispose();offProfileFailures?.();profiles?.dispose();previewLife.abort();signinStorage?.dispose();signIn?.dispose();memberResetCodes?.dispose();people?.invitations?.dispose();people?.client.dispose();for (const listener of authorityListeners) {try {listener();} catch { /* Retire the other captured readers too. */ }}authorityListeners.clear();dashboard?.dispose();libraries?.dispose();privacy.dispose();intros?.dispose();server?.dispose();troubleshooting?.dispose();playbackProblems?.dispose();live?.dispose();integrationKeys?.dispose();webhooks?.dispose();updates?.dispose();missing?.dispose();hardware?.dispose();remoteForm?.dispose();certificate?.dispose();controller.dispose();};
  // Each setup owns and retires exactly its own sources. No shared "latest"
  // slot participates in cleanup, including the owner's update reader.
  }, [context, retry, routeScope, routeSection, hasPreferences, reportTimeZone,
    buildPrimary, buildAccount, buildServer, buildPeople, buildSignIn, buildKind, buildRole, buildServerId, buildAuthority, buildAccountId, buildProfileId]);
  const selected = protectedContext && owned && sameBuildInputs(owned.inputs, inputs) && owned.current() ? owned : undefined;
  if (!selected) return null;
  const navigate = (next: Parameters<typeof SettingsScreen>[0]['route']) => {if (selected.current()) void router.navigate({to: '/settings', search: () => next});};
  const settings = failure ? {...selected.settings, section: () => <SettingsPageFailure retry={() => {if (selected.current()) setRetry(value => value + 1);}} home={selected.settings.openHome} />} : selected.settings;
  // A URL that names its scope or section was a link to that page (a Dashboard
  // or storage alert), so the compact layout opens the page, not the list.
  const openDetail = typeof raw.scope === 'string' || typeof raw.section === 'string';
  return <><SecureAddressProvider api={selected.api} isCurrent={selected.current} active={route.scope === 'server' && route.section === 'signin' && selected.settings.role === 'owner'}>
    <SettingsScreen bindings={settings} route={route} navigate={navigate} openDetail={openDetail} />
  </SecureAddressProvider>
    {profilePIN && selected.profiles === profilePIN.source && <ProfilePINDialog key={JSON.stringify([profilePIN.target.id, profilePIN.target.revision, profilePIN.remove])}
      request={profilePIN} isCurrent={() => selected.profilePINCurrent(profilePIN) && selected.profiles === profilePIN.source && !!profilePIN.source.getSnapshot().document} onClose={() => selected.closeProfilePIN(profilePIN)} onFailure={() => {if (selected.profilePINCurrent(profilePIN)) setFailure(true);}} />}
  </>;
}

export default defineWebScreen({id: 'settings', component: SettingsEntry, validateSearch: raw => settingsRoute(raw, () => true)});
