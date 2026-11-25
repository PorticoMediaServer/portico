import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {dependsOn} from '@core/freshness-hub.ts';
import type {ReportContext} from '@core/report-context.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {forwardFacts} from '../../shell/fact-source';
import {shellLanguage} from '../../shell/settings';
import {settingsRoute} from '../../kit/settings/search';
import {SettingsOwnerOnly, SettingsPageFailure} from '../../kit/settings';
import {settingsPlatform} from '../../platform/settings-platform';
import type {DashboardBindings} from '../dashboard';
import {SettingsScreen, type SettingsBindings, type SettingsPhoneLead} from './Settings';
import {SecureAddressProvider} from './SecureAddressProvider';
import {isFatalSettingsError, resolveSettingsLocation, type SettingsSectionAccess} from './controller';
import {mountQuietInput} from './quiet-refresh';
import serverStyles from './server/server.module.css';
import type {ProfileSettingsSource} from './account-profile/source';
import {ProfilePINDialog, type ProfilePINRequest} from './account-profile/ProfilePINControl';
import {createFencedSettingsApi} from './route-sections/fenced-api';
import {assembleSettingsRouteSections} from './route-sections';
import type {SettingsRouteEnv} from './route-sections/types';

/** A confirmed transfer permanently spends this owner Settings context. Only
 * the shell's actual authority reconciliation can supply a replacement. */
const transferredOwnerContexts = new WeakSet<ShellAssemblyContext>();
const noPreferences = () => () => {};
const sameBuildInputs = (a: readonly unknown[], b: readonly unknown[]) =>
  a.length === b.length && a.every((value, index) => Object.is(value, b[index]));

const settingsBuildFacts = (context?: ShellAssemblyContext) => {
  const identity = context?.facts.identity, viewer = context?.session().viewer;
  return [identity?.primaryProfile, identity?.capabilities?.manageAccount, identity?.capabilities?.manageServer,
    identity?.capabilities?.managePeople, identity?.capabilities?.manageSignIn, identity?.signIn,
    viewer?.role, viewer?.serverId, viewer?.authority, viewer?.accountId, viewer?.profileId] as const;
};

function settingsAccess(context: ShellAssemblyContext, platform = settingsPlatform()): SettingsSectionAccess {
  const identity = context.facts.identity, viewer = context.session().viewer;
  const role = viewer.role === 'owner' || viewer.role === 'admin' ? viewer.role : 'member';
  return {
    role,
    mainProfile: identity.primaryProfile === true,
    capabilities: {
      manageServer: identity.capabilities?.manageServer === true,
      managePeople: identity.capabilities?.managePeople === true,
      manageSignIn: identity.capabilities?.manageSignIn === true,
    },
    environment: {...(platform?.environment ?? {platform: 'web' as const, os: 'browser' as const}), role, signIn: identity.signIn},
  };
}

type SettingsBuild = Readonly<{
  context: ShellAssemblyContext;
  inputs: readonly unknown[];
  current: () => boolean;
  /** The build's own captured transport, so a holder read keeps this same fence. */
  api: ShellAssemblyContext['api'];
  settings: SettingsBindings;
  dashboard?: DashboardBindings;
  profiles?: ProfileSettingsSource;
  reportActionError: (error?: unknown) => void;
  profilePINCurrent: (request: ProfilePINRequest) => boolean;
  closeProfilePIN: (request: ProfilePINRequest) => void;
}>;

export function SettingsEntry() {
  const protectedContext = useProtectedWebScreenContext();
  const location = useRouterState({select: state => state.location});
  const [owned, setOwned] = useState<SettingsBuild>();
  const [profilePIN, setProfilePIN] = useState<ProfilePINRequest>();
  // Fatal page-level failure (failed initial load, lost role, or management PIN refusal).
  const [failure, setFailure] = useState<false | Readonly<{error: unknown}>>(false), [retry, setRetry] = useState(0);
  // Non-fatal inline mutation error kept inside the page without collapsing to SettingsPageFailure.
  const [actionError, setActionError] = useState<false | Readonly<{error: unknown}>>(false);
  const context = protectedContext?.context;
  useSyncExternalStore(context?.factsSource?.subscribe ?? noPreferences, () => context?.factsSource?.getSnapshot() ?? context?.facts, () => undefined);
  // The same confirmed reader drives calendar lifetime. A zone change retires old report results.
  const preferences = useSyncExternalStore(context?.preferences?.subscribe ?? noPreferences, () => context?.preferences?.getSnapshot('web'), () => undefined);
  const reportTimeZone = preferences && shellLanguage(preferences).region.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
  const raw = Object.fromEntries(new URLSearchParams(location.searchStr));
  const platform = settingsPlatform();
  const access = context ? settingsAccess(context, platform) : undefined;
  const resolution = access ? resolveSettingsLocation(location, access) : undefined;
  const unauthorized = resolution?.unauthorized === true;
  const route = resolution && !resolution.unauthorized ? resolution.route : undefined;
  const facts = settingsBuildFacts(context);
  const [buildPrimary, buildAccount, buildServer, buildPeople, buildSignIn, buildKind, buildRole, buildServerId, buildAuthority, buildAccountId, buildProfileId] = facts;
  const routeScope = route?.scope, routeSection = route?.section, hasPreferences = !!preferences;
  // Ordinary preference confirmations and row-only deep links keep their controller, forms and outstanding intents.
  const inputs = [context, retry, unauthorized, routeScope, routeSection, !!preferences, reportTimeZone, ...facts] as const;
  const reportCarry = useRef<Readonly<{inputs: readonly unknown[]; report: ReportContext}> | undefined>(undefined);
  const mediaRuntime = protectedContext?.binding.playback?.runtime;
  // Setup samples these committed ports once; the exact scalar owner keys below govern retirement.
  const setupPorts = useRef({preferences, route, unauthorized, facts, inputs, mediaRuntime});
  useLayoutEffect(() => {setupPorts.current = {preferences, route, unauthorized, facts, inputs, mediaRuntime};});
  useLayoutEffect(() => {
    const {preferences, route, unauthorized, facts, inputs, mediaRuntime} = setupPorts.current;
    const reader = context?.preferences, snapshot = preferences;
    if (!context || !reader || !snapshot || unauthorized || !route || !context.isCurrent() || transferredOwnerContexts.has(context)) return;
    let active = true, inputActive = false, inputRevision = 0;
    const inputListeners = new Set<() => void>();
    const inputActivity = {
      active: () => inputActive,
      revision: () => inputRevision,
      subscribe: (listener: () => void) => {inputListeners.add(listener); return () => {inputListeners.delete(listener);};},
    };
    const lifetime = new AbortController(), authorityListeners = new Set<() => void>();
    let preferencesAvailable = true, confirmedZone = reportTimeZone;
    const offPreferenceFence = reader.subscribe(() => {
      const confirmed = reader.getSnapshot('web');
      preferencesAvailable = !!confirmed;
      confirmedZone = confirmed && shellLanguage(confirmed).region.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
    });
    const current = () => {
      if (!active || transferredOwnerContexts.has(context) || !context.isCurrent() || !sameBuildInputs(facts, settingsBuildFacts(context))) return false;
      const loc = router.state.location;
      if (loc.pathname !== '/settings' && loc.pathname !== '/dashboard') return false;
      const selected = resolveSettingsLocation(loc, settingsAccess(context, settingsPlatform()));
      return !selected.unauthorized && selected.route.scope === route.scope && selected.route.section === route.section && preferencesAvailable && confirmedZone === reportTimeZone;
    };
    const assert = () => {if (!current()) throw new DOMException('Settings build retired', 'AbortError');};
    const signal = (input?: AbortSignal | null) => input ? AbortSignal.any([lifetime.signal, input]) : lifetime.signal;
    const originalApi = context.api;
    const api = createFencedSettingsApi(originalApi, assert, signal);
    const buildContext = forwardFacts(context, {api, isCurrent: current});
    const reportError = (error?: unknown) => {if (current()) setFailure({error});};
    const reportActionError = (error?: unknown) => {
      if (!current()) return;
      if (isFatalSettingsError(error)) {setFailure({error}); return;}
      setActionError({error});
    };
    const language = snapshot ? shellLanguage(snapshot) : defaultI18n;
    const identity = context.facts.identity, session = context.session(), viewer = session.viewer;
    const role = viewer.role === 'owner' || viewer.role === 'admin' ? viewer.role : 'member';
    const viewerId = JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId]);
    const env: SettingsRouteEnv = {
      context, buildContext, originalApi, api, route, role, identity, viewer, session, viewerId,
      uiLocale: language.locale, appLocale: language.region.formatLocale, platform: settingsPlatform(),
      current, assert, signal, reportError, reportActionError, inputActivity, authorityListeners,
      mediaRuntime, reportTimeZone, facts,
      openHome: () => {if (current()) void router.navigate({to: '/'});},
      openSettings: section => {if (current()) void router.navigate({to: '/settings', search: () => ({scope: 'server', section})});},
      openSettingsRow: target => {if (current()) void router.navigate({to: '/settings', search: () => target});},
    };
    const retireTransferredContext = () => {
      // A confirmed transfer permanently spends this exact owner Settings build immediately.
      transferredOwnerContexts.add(context);
      active = false; lifetime.abort();
      for (const listener of authorityListeners) {try {listener();} catch { /* Retire every captured command. */ }}
      authorityListeners.clear();
      setOwned(held => held?.current === current ? undefined : held);
    };

    const quietInterests: ReturnType<typeof mountQuietInput>[] = [];
    let notifyQuietInput: ((active: boolean) => void) | undefined;
    const assembly = assembleSettingsRouteSections({
      env, preferences: snapshot, reader, setProfilePIN, reportCarry, retireTransferredContext,
      setQuietOpen: open => {for (const interest of quietInterests) interest.setOpen(open);},
      onInputActive: value => {
        if (!current() || inputActive === value) return;
        inputActive = value; ++inputRevision;
        notifyQuietInput?.(value);
        for (const listener of inputListeners) listener();
      },
    });
    notifyQuietInput = assembly.setQuietInputActive;
    quietInterests.push(...assembly.quietInputs.map(input => mountQuietInput(
      context.freshness.hub,
      `settings/${route.scope}/${route.section}`,
      [dependsOn.settings('server'), dependsOn.libraries],
      {
        ...input,
        blocked: () => inputActive || !!input.blocked?.(),
        subscribe: listener => {const off = input.subscribe?.(listener), offInput = inputActivity.subscribe(listener); return () => {off?.(); offInput();};},
      },
    )));

    setFailure(false);
    setActionError(false);
    setOwned({
      context, api, inputs, current, settings: assembly.settings, dashboard: assembly.dashboard,
      profiles: assembly.profiles, reportActionError,
      profilePINCurrent: assembly.profilePINCurrent, closeProfilePIN: assembly.closeProfilePIN,
    });
    return () => {
      active = false;
      for (const interest of quietInterests) interest.dispose();
      inputListeners.clear();
      offPreferenceFence();
      lifetime.abort();
      assembly.dispose();
      for (const listener of authorityListeners) {try {listener();} catch { /* Retire the other captured readers too. */ }}
      authorityListeners.clear();
    };
  // Each setup owns and retires exactly its own sources. No shared "latest" slot participates in cleanup.
  }, [context, retry, unauthorized, routeScope, routeSection, hasPreferences, reportTimeZone,
    buildPrimary, buildAccount, buildServer, buildPeople, buildSignIn, buildKind, buildRole, buildServerId, buildAuthority, buildAccountId, buildProfileId]);

  if (!protectedContext || !context) return null;
  if (unauthorized) {
    const openMySettings = () => {void router.navigate({to: '/settings', search: () => ({scope: 'account', section: 'profile'})});};
    const home = () => {void router.navigate({to: '/'});};
    return <SettingsOwnerOnly serverName={context.facts.identity.serverName} openMySettings={openMySettings} home={home} />;
  }
  const selected = owned && sameBuildInputs(owned.inputs, inputs) && owned.current() ? owned : undefined;
  if (!selected || !route) return null;
  const navigate = (next: Parameters<typeof SettingsScreen>[0]['route']) => {if (selected.current()) void router.navigate({to: '/settings', search: () => next});};
  const pageFailure = failure ? <SettingsPageFailure retry={() => {if (selected.current()) setRetry(value => value + 1);}} home={selected.settings.openHome} error={failure.error}
    server={route.scope === 'server' ? {name: selected.settings.serverName, openMySettings: () => navigate({scope: 'account', section: 'profile'})} : undefined} /> : undefined;
  const inlineErrorText = actionError ? ((actionError.error as {code?: unknown} | null)?.code === 'factor_setup_required'
    ? defaultI18n.t('error.factorSetupRequired')
    : defaultI18n.t('next.states.pageFailedBody')) : undefined;
  const inlineError = inlineErrorText ? <p role="alert" className={serverStyles.backupFailure}>{inlineErrorText}</p> : undefined;
  const openDetail = typeof raw.scope === 'string' || typeof raw.section === 'string';
  return <>
    <SecureAddressProvider api={selected.api} isCurrent={selected.current} active={route.scope === 'server' && route.section === 'signin' && selected.settings.role === 'owner'}>
      <SettingsScreen bindings={selected.settings} route={route} navigate={navigate} openDetail={openDetail} phoneLead={phoneLead(selected.context.facts.identity)} pageFailure={pageFailure} actionError={inlineError} />
    </SecureAddressProvider>
    {profilePIN && selected.profiles === profilePIN.source && <ProfilePINDialog key={JSON.stringify([profilePIN.target.id, profilePIN.remove])}
      request={profilePIN}
      isCurrent={() => selected.profilePINCurrent(profilePIN) && selected.profiles === profilePIN.source && !!profilePIN.source.getSnapshot().document}
      onClose={() => selected.closeProfilePIN(profilePIN)}
      onFailure={error => {if (selected.profilePINCurrent(profilePIN)) selected.reportActionError(error);}} />}
  </>;
}

/** At phone width the list starts with who you are and the four quick tiles (phone-web 9, phone-app 16). */
function phoneLead(identity: ShellAssemblyContext['facts']['identity']): SettingsPhoneLead {
  const t = defaultI18n.t, go = (to: '/profiles' | '/servers' | '/notifications' | '/watch-together') => () => {void router.navigate({to});};
  return {
    profile: {id: identity.viewer.profileId, name: identity.profileName, initial: Array.from(identity.profileName.trim())[0] ?? '', art: identity.profileArt},
    description: t('settings.signedInAs', {server: identity.serverName, username: identity.username || identity.profileName}),
    shortcuts: [
      {id: 'profile', icon: 'i-switch', label: t('next.shell.switchProfile'), onAction: go('/profiles')},
      {id: 'server', icon: 'i-server', label: t('next.shell.switchServer'), onAction: go('/servers')},
      {id: 'notifications', icon: 'i-bell', label: t('next.shell.notifications'), onAction: go('/notifications')},
      {id: 'together', icon: 'i-people', label: t('next.shell.watchTogether'), onAction: go('/watch-together')},
    ],
  };
}

export default defineWebScreen({
  id: 'settings',
  component: SettingsEntry,
  // The bare /settings stays bare: at phone width it is the list, and only a link that names a page opens one.
  validateSearch: raw => raw.scope === undefined && raw.section === undefined && raw.row === undefined ? {} : settingsRoute(raw, () => true),
});
