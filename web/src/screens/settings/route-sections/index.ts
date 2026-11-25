import type {Dispatch, SetStateAction} from 'react';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {OwnerGeneralSettings} from '@core/server-admin/general-settings.ts';
import {OwnerStreamingSettings} from '@core/server-admin/streaming-settings.ts';
import {readOwnerSignInConfiguration} from '@core/sign-in-policy.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {PreferencesReader} from '@core/preferences-reader.ts';
import {subscribeFacts} from '../../../shell/fact-source';
import type {DashboardBindings} from '../../dashboard';
import {SettingsController, type SettingsControllerOptions} from '../controller';
import {createSettingsBindings, forwardSettingsBindings} from '../bindings';
import {readingSettingsAdapter} from '../reading-adapter';
import {hasWebPreferenceEffect} from '../effects';
import {presetEnginesAvailable} from '../server/engines';
import type {SettingsBindings} from '../Settings';
import type {QuietInput} from '../quiet-refresh';
import type {ProfilePINRequest} from '../account-profile/ProfilePINControl';
import type {ProfileSettingsSource} from '../account-profile/source';
import type {ServerSectionGateInput} from './access-gate';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';
import {createAccountProfileSection} from './account-profile';
import {createAccountPreferencesSection} from './account-preferences';
import {createServerGeneralStreamingSection} from './server-general-streaming';
import {createServerLibrariesSection} from './server-libraries';
import {createServerSigninPeopleSection} from './server-signin-people';
import {createServerRemoteMetadataSection} from './server-remote-metadata';
import {createServerMediaOpsSection, type ReportCarrySlot} from './server-media-ops';

export type SettingsRouteAssembly = Readonly<{
  settings: SettingsBindings;
  dashboard?: DashboardBindings;
  profiles?: ProfileSettingsSource;
  profilePINCurrent: (request: ProfilePINRequest) => boolean;
  closeProfilePIN: (request: ProfilePINRequest) => void;
  quietInputs: readonly QuietInput[];
  setQuietInputActive: (active: boolean) => void;
  dispose: () => void;
}>;

/** Compose all Settings route section adapters around a single fenced SettingsController. */
export function assembleSettingsRouteSections(params: Readonly<{
  env: SettingsRouteEnv;
  preferences: PreferenceSnapshot;
  reader: PreferencesReader;
  setProfilePIN: Dispatch<SetStateAction<ProfilePINRequest | undefined>>;
  reportCarry: ReportCarrySlot;
  retireTransferredContext: () => void;
  setQuietOpen: (open: boolean) => void;
  onInputActive: (active: boolean) => void;
}>): SettingsRouteAssembly {
  const {env, preferences, reader, setProfilePIN, reportCarry, retireTransferredContext, setQuietOpen, onInputActive} = params;
  const {api, context, buildContext, route, role, identity, viewer, viewerId, platform, current, reportError} = env;
  const gate: ServerSectionGateInput = {
    role,
    primaryProfile: identity.primaryProfile === true,
    capabilities: identity.capabilities,
    signIn: identity.signIn,
    authority: viewer.authority,
  };

  const accountProfile = createAccountProfileSection(env, setProfilePIN);
  const accountPreferences = createAccountPreferencesSection(env);
  const remoteMetadata = createServerRemoteMetadataSection(env, gate);
  const signinPeople = createServerSigninPeopleSection(env, gate, retireTransferredContext);
  const preSurfaces: SettingsSectionSurface[] = [accountProfile.surface, accountPreferences, remoteMetadata.surface, signinPeople.surface];

  // One row's engine. A preset row is answered by the rule below instead, so its own missing binding never decides for the rows it sets.
  const engine = (row: SettingsRow) => {
    for (const surface of preSurfaces) {
      const result = surface.hasEngine?.(row);
      if (result !== undefined) return result;
    }
    const binding = settingsBinding(row.id);
    if (!binding) return row.id === 'device.name' && !!platform?.device?.getSnapshot().facts.name || row.id === 'device.appVersion' && !!platform?.device?.getSnapshot().facts.appVersion;
    if (binding.form === 'preferences') return hasWebPreferenceEffect(row);
    return binding.form !== 'library' && binding.form !== 'remote' && binding.form !== 'managedRoute';
  };

  const options: SettingsControllerOptions = {
    api,
    readConfiguration: route.scope === 'server' && route.section === 'signin' ? abort => readOwnerSignInConfiguration(api, abort) : undefined,
    queries: context.freshness.queries,
    serverId: viewer.serverId,
    viewerId,
    ownerSettings: remoteMetadata.remoteSettings ?? (route.scope === 'server' && route.section === 'general' ? new OwnerGeneralSettings(api) : undefined),
    streaming: route.scope === 'server' && route.section === 'streaming' ? new OwnerStreamingSettings(api) : undefined,
    role,
    mainProfile: identity.primaryProfile === true,
    capabilities: {
      manageServer: identity.capabilities?.manageServer === true,
      managePeople: identity.capabilities?.managePeople === true,
      manageSignIn: identity.capabilities?.manageSignIn === true,
    },
    preferences,
    reader,
    isCurrent: current,
    reportError,
    reading: readingSettingsAdapter,
    environment: {...(platform?.environment ?? {platform: 'web' as const, os: 'browser' as const}), role, signIn: identity.signIn},
    device: platform?.device,
    eligibility: {
      library: () => undefined,
      hasEngine: row => preSurfaces.some(surface => surface.directEngine?.(row)) ? true : presetEnginesAvailable(row, engine) ?? engine(row),
    },
  };
  const controller = new SettingsController(options);
  const generalStreaming = createServerGeneralStreamingSection(env, controller, gate);
  const librariesSection = createServerLibrariesSection(env, gate);
  const mediaOps = createServerMediaOpsSection(env, controller, gate, reportCarry, !!preferences);
  const surfaces: SettingsSectionSurface[] = [...preSurfaces, generalStreaming, librariesSection, mediaOps.surface];

  const source = createSettingsBindings(controller, options, identity, {
    openHome: env.openHome,
    signIn: signinPeople.signIn,
    special: (row, state, rowContext, openPanel) => {
      for (const surface of surfaces) {
        const rendered = surface.special?.(row, state, rowContext, openPanel, controller);
        if (rendered !== undefined) return rendered;
      }
      return null;
    },
    section: (selected, openPanel, renderRows) => {
      for (const surface of surfaces) {
        const rendered = surface.section?.(selected, openPanel, renderRows, controller);
        if (rendered !== undefined) return rendered;
      }
      return undefined;
    },
  }, {identity: () => buildContext.facts.identity, subscribe: listener => subscribeFacts(buildContext, listener), getSnapshot: () => buildContext.facts});

  const settings = forwardSettingsBindings(source, {
    setOpen: setQuietOpen,
    inputActive: onInputActive,
    panelIntro: selected => {
      for (const surface of surfaces) {const node = surface.panelIntro?.(selected); if (node !== undefined) return node;}
      return null;
    },
    panelHeader: (selected, title) => {
      for (const surface of surfaces) {const node = surface.panelHeader?.(selected, title, controller); if (node !== undefined) return node;}
      return null;
    },
    subscribe: listener => {
      const cleanups = [source.subscribe(listener), ...surfaces.map(surface => surface.subscribe?.(listener)).filter((fn): fn is () => void => !!fn)];
      return () => {for (const off of cleanups) off();};
    },
    change: (row, value, selected) => {
      if (!current()) return;
      for (const surface of surfaces) if (surface.change?.(row, value, selected)) return;
      source.change(row, value, selected);
    },
    getSnapshot: section => {
      for (const surface of surfaces) {const snap = surface.getSnapshot?.(section, source); if (snap !== undefined) return snap;}
      return source.getSnapshot(section);
    },
    load: section => {
      for (const surface of surfaces) {const task = surface.load?.(section, source, controller); if (task !== undefined) return task;}
      return source.load(section);
    },
    eligible: (row, selected) => {
      for (const surface of surfaces) {const ok = surface.eligible?.(row, selected, controller); if (ok !== undefined) return ok;}
      return source.eligible(row, selected);
    },
    reset: (row, selected) => source.reset(row, selected),
    rowState: (row, selected) => {
      for (const surface of surfaces) {const state = surface.rowState?.(row, selected, controller, source); if (state !== undefined) return state;}
      return source.rowState(row, selected);
    },
  });
  const publishedSettings = forwardSettingsBindings(settings, {
    change: (...args) => {if (current()) settings.change(...args);},
    reset: (...args) => {if (current()) settings.reset(...args);},
    load: (...args) => current() ? settings.load(...args) : Promise.resolve(),
    ...(settings.loadContext ? {loadContext: (...args: Parameters<NonNullable<SettingsBindings['loadContext']>>) => current() ? settings.loadContext!(...args) : Promise.resolve()} : {}),
  });

  const section = settingsSpec.find(item => item.scope === route.scope && item.id === route.section)!;
  const quietInputs: QuietInput[] = [
    ...(route.scope === 'server' && ['signin', 'live', 'metadata', 'remote-sources'].includes(route.section) ? [] : controller.quietInputs(section)),
    ...surfaces.flatMap(surface => surface.quietInputs?.() ?? []),
  ];

  return {
    settings: publishedSettings,
    dashboard: mediaOps.dashboard,
    profiles: accountProfile.profiles,
    profilePINCurrent: accountProfile.profilePINCurrent,
    closeProfilePIN: accountProfile.closeProfilePIN,
    quietInputs,
    setQuietInputActive: value => {
      controller.setQuietInputActive(value);
      for (const surface of surfaces) surface.setQuietInputActive?.(value);
    },
    dispose: () => {
      for (const surface of surfaces) surface.dispose?.();
      controller.dispose();
    },
  };
}
