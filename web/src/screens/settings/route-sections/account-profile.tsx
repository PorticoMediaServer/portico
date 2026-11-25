import {Fragment, type Dispatch, type SetStateAction} from 'react';
import {uploadOwnerProfileAvatar} from '@core/owner-upload-requests.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {SettingsStringList} from '../../../kit/settings/SettingsStringList';
import {ProfileSettingsSource} from '../account-profile/source';
import {profileSettingState, changeProfileSetting} from '../account-profile/rows';
import {ProfileList} from '../account-profile/ProfileList';
import {ProfileDelete} from '../account-profile/ProfileDelete';
import {ProfilePicture} from '../account-profile/ProfilePicture';
import {ProfileSignOut} from '../account-profile/ProfileSignOut';
import {ProfileForgotPIN} from '../account-profile/ProfileForgotPIN';
import {ProfileDevicesSource} from '../account-profile/devices';
import {ProfileDevices} from '../account-profile/ProfileDevices';
import {ProfileOtherSignOut} from '../account-profile/ProfileOtherSignOut';
import {ProfileOtherDeviceSignOut} from '../account-profile/sign-out-others';
import {ProfileRestrictionsSource} from '../account-profile/restrictions';
import {isProfileRestriction, restrictionRow, changeRestriction} from '../account-profile/restriction-rows';
import {observeProfileFailures} from '../account-profile/failures';
import type {ProfilePINRequest} from '../account-profile/ProfilePINControl';
import {ProfileForgetDevices} from '../account-profile/ProfileForgetDevices';
import {ProfileSignIn, type ProfileSecurityFacts} from '../account-profile/ProfileSignIn';
import {profileSecurityTransport} from '../account-profile/security-transport';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

const profileRowIds = new Set([
  'profile.list', 'profile.name', 'profile.color', 'profile.username',
  'profile.pin', 'profile.pinChange', 'profile.pinForget', 'profile.delete', 'profile.picture',
]);
const profileSecurityRowIds = new Set(['profile.email', 'profile.password', 'profile.twoStep', 'profile.recoveryCodes']);

export type AccountProfileSectionHandle = Readonly<{
  surface: SettingsSectionSurface;
  profiles?: ProfileSettingsSource;
  profilePINCurrent: (request: ProfilePINRequest) => boolean;
  closeProfilePIN: (request: ProfilePINRequest) => void;
}>;

/** Account › Profile section: profile CRUD, avatar, PIN, restrictions, devices, and account security. */
export function createAccountProfileSection(
  env: SettingsRouteEnv,
  setProfilePIN: Dispatch<SetStateAction<ProfilePINRequest | undefined>>,
): AccountProfileSectionHandle {
  const {api, originalApi, buildContext, context, route, role, identity, viewer, current, signal, reportError, reportActionError} = env;
  const profiles = route.scope === 'account' && route.section === 'profile' && viewer.authority === 'local'
    ? new ProfileSettingsSource(api, {serverId: viewer.serverId, accountId: viewer.accountId, profileId: viewer.profileId}, current, async (path, body, contentType, abort) => {
      if (!current()) throw new Error('shell_authority_retired');
      const response = await uploadOwnerProfileAvatar(api, path, body, contentType, abort);
      if (!response.ok) throw Object.assign(new Error('Profile picture could not be saved.'), {status: response.status});
      return response.json();
    }, buildContext.observeDirect) : undefined;
  const restrictions = profiles ? new ProfileRestrictionsSource(api, profiles, current) : undefined;
  // The profile page redraws when the selected profile's limits arrive, not only when the profile list changes.
  let profilePage: {profiles?: unknown; limits?: unknown; value?: ReturnType<NonNullable<typeof profiles>['getSnapshot']>} = {};
  const profilePageSnapshot = () => {
    const list = profiles!.getSnapshot(), limits = restrictions?.getSnapshot();
    if (profilePage.profiles !== list || profilePage.limits !== limits) profilePage = {profiles: list, limits, value: Object.freeze({...list})};
    return profilePage.value!;
  };
  // A Tag name nobody has is told in place on the Blocked Tags row; any other refused save is reported as before.
  const limitsFailure = (error: unknown) => {if ((error as {code?: unknown} | null)?.code !== 'unknown_tag') reportActionError(error);};
  const profileDevices = profiles && identity.signIn === 'local' ? new ProfileDevicesSource(api, profiles, current) : undefined;
  const otherDeviceSignOut = profiles ? new ProfileOtherDeviceSignOut(api, profiles, current) : undefined;
  const offProfileFailures = profiles ? observeProfileFailures(profiles, current, reportError) : undefined;
  const profileSecurityCurrent = () => current() && route.scope === 'account' && route.section === 'profile' &&
    identity.primaryProfile === true && identity.capabilities?.manageAccount === true && viewer.authority === 'local' &&
    profiles?.getSnapshot().document?.canManage === true;
  const securityCommands = profiles ? profileSecurityTransport(originalApi, () => {
    if (!profileSecurityCurrent()) throw new DOMException('Account security retired', 'AbortError');
  }, signal) : undefined;
  let securityFacts: ProfileSecurityFacts = {};
  const securityFactsChanged = (next: ProfileSecurityFacts) => {if (profileSecurityCurrent()) securityFacts = next;};
  const isProfileRow = (id: string) => !!profiles && profileRowIds.has(id);
  let pinRequest: ProfilePINRequest | undefined;
  const profilePINCurrent = (request: ProfilePINRequest) => current() && pinRequest === request;
  const closeProfilePIN = (request: ProfilePINRequest) => {
    if (pinRequest === request) {
      pinRequest = undefined;
      setProfilePIN(held => held === request ? undefined : held);
    }
  };
  const openProfilePIN = (remove: boolean) => {
    const target = profiles?.selected(), document = profiles?.getSnapshot().document;
    if (current() && profiles && target && document?.canManage && document.capabilities.manageAccount) {
      pinRequest = {source: profiles, target, remove, trigger: globalThis.document.activeElement as HTMLElement | null};
      setProfilePIN(pinRequest);
    }
  };
  const isProfileSection = (section: Readonly<{scope: string; id: string}>) => section.scope === 'account' && section.id === 'profile';

  const surface: SettingsSectionSurface = {
    subscribe: listener => {
      const offProfiles = profiles?.subscribe(listener), offLimits = restrictions?.subscribe(listener);
      return () => {offProfiles?.(); offLimits?.();};
    },
    getSnapshot: section => isProfileSection(section) && profiles ? profilePageSnapshot() : undefined,
    load: (section, source) => isProfileSection(section) && profiles ? Promise.all([profiles.load(), source.load(section)]).then(() => undefined) : undefined,
    change: (row, value) => {
      if (restrictions && isProfileRestriction(row.id)) {void changeRestriction(restrictions, row.id, value)?.catch(limitsFailure); return true;}
      if (profiles && row.id === 'profile.pin' && typeof value === 'boolean') {openProfilePIN(!value); return true;}
      if (profiles && isProfileRow(row.id)) {void changeProfileSetting(profiles, row.id, value)?.catch(reportActionError); return true;}
      return false;
    },
    eligible: (row, selected) => {
      if (profileSecurityRowIds.has(row.id)) return profileSecurityCurrent();
      if (row.id === 'profile.devices') return !!profileDevices && current() && isProfileSection(selected.section);
      if (row.id === 'profile.pinForgot') return !!profiles && identity.signIn === 'local' && current();
      if (row.id === 'profile.signOutElsewhere') return !!otherDeviceSignOut && current() && isProfileSection(selected.section);
      if (row.id === 'profile.signOut') return current() && isProfileSection(selected.section);
      if (restrictions && isProfileRestriction(row.id)) return current() && isProfileSection(selected.section);
      if (isProfileRow(row.id)) return current() && isProfileSection(selected.section);
      return undefined;
    },
    rowState: row => {
      if (profileSecurityRowIds.has(row.id)) {
        return {
          present: profileSecurityCurrent() && (row.id !== 'profile.recoveryCodes' || securityFacts.mfa === true),
          value: row.id === 'profile.twoStep' ? securityFacts.mfa : undefined,
          help: row.id === 'profile.email' ? securityFacts.email : undefined,
        };
      }
      if (row.id === 'profile.devices') return {present: !!profileDevices && current() && profiles?.getSnapshot().document?.canManage === true};
      if (row.id === 'profile.pinForgot') return {present: !!profiles && identity.signIn === 'local' && current() && profiles.getSnapshot().document?.canManage === false && profiles.selected()?.pinRequired === true};
      if (row.id === 'profile.signOutElsewhere') return {present: !!otherDeviceSignOut && current() && profiles?.getSnapshot().document?.canManage === true, disabled: profiles?.getSnapshot().busy};
      if (row.id === 'profile.signOut') return {present: current()};
      if (restrictions && profiles && isProfileRestriction(row.id)) return restrictionRow(restrictions, profiles, row.id, role === 'owner')!;
      if (profiles && isProfileRow(row.id)) {
        return row.id === 'profile.list' ? {present: current() && !!profiles.getSnapshot().document} : profileSettingState(profiles, row.id)!;
      }
      return undefined;
    },
    special: (row, state) => {
      if (row.id === 'profile.devices' && profileDevices) return <ProfileDevices source={profileDevices} isCurrent={current} onFailure={reportActionError} signOut={context.signOut} />;
      if (row.id === 'profile.pinForgot' && profiles && identity.signIn === 'local') return <ProfileForgotPIN source={profiles} api={api} selectedFamilyId={() => context.session().sessionFamilyId} isCurrent={current} />;
      if (row.id === 'profile.signOutElsewhere' && otherDeviceSignOut) return <ProfileOtherSignOut command={otherDeviceSignOut} isCurrent={current} disabled={state.disabled} onFailure={reportActionError} />;
      if (row.id === 'profile.signOut') return <ProfileSignOut serverName={buildContext.facts.identity.serverName} isCurrent={current} signOut={context.signOut} />;
      if (row.id === 'profile.hiddenLabels' && restrictions) {
        return <SettingsStringList label={defaultI18n.t(row.label)} values={(profiles ? restrictionRow(restrictions, profiles, row.id)?.value as string[] | undefined : undefined) ?? []} addLabel={defaultI18n.t(row.control.addLabel!)} disabled={state.disabled} onChange={value => {void changeRestriction(restrictions, row.id, value)?.catch(limitsFailure);}} />;
      }
      if (row.id === 'profile.picture' && profiles) return <ProfilePicture source={profiles} isCurrent={current} onFailure={reportActionError} />;
      if (row.id === 'profile.delete' && profiles) return <ProfileDelete source={profiles} isCurrent={current} onFailure={reportActionError} />;
      if (row.id === 'profile.pinForget' && profiles) return <ProfileForgetDevices source={profiles} isCurrent={current} onFailure={reportActionError} />;
      if (row.id === 'profile.pinChange' && profiles) return <Button size="small" variant="glass" disabled={state.disabled} onClick={() => openProfilePIN(false)}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
      if (row.id === 'profile.list' && profiles) {
        return <ProfileList source={profiles} isCurrent={current} onFailure={reportActionError} onSelect={() => {
          requestAnimationFrame(() => {if (current()) document.getElementById('profile.name')?.querySelector<HTMLInputElement>('input')?.focus();});
        }} />;
      }
      return undefined;
    },
    section: (selected, _openPanel, renderRows) => {
      if (!isProfileSection(selected.section) || !profiles || !securityCommands) return undefined;
      return <>
        {selected.section.groups?.map((group, index) => group.rows.some(row => row.id === 'profile.password')
          ? <ProfileSignIn key={index} session={context.session} profiles={profiles} api={api} commandApi={securityCommands} signIn={identity.signIn === 'portico' ? 'portico' : 'local'} isCurrent={profileSecurityCurrent} onFacts={securityFactsChanged} />
          : <Fragment key={index}>{renderRows({groups: [group]})}</Fragment>)}
        {selected.section.advanced ? renderRows({advanced: selected.section.advanced}) : null}
      </>;
    },
    quietInputs: () => profiles ? [{
      id: 'profiles',
      refresh: profiles.refresh,
      cancel: profiles.invalidateQuiet,
      subscribe: profiles.subscribe,
      blocked: () => profiles.quietBlocked() || profiles.getSnapshot().loading,
      settlement: () => profiles.getSnapshot().document,
    }] : [],
    setQuietInputActive: value => {profiles?.setQuietInputActive(value);},
    dispose: () => {
      pinRequest = undefined;
      setProfilePIN(held => held?.source === profiles ? undefined : held);
      profileDevices?.dispose();
      otherDeviceSignOut?.dispose();
      restrictions?.dispose();
      offProfileFailures?.();
      profiles?.dispose();
    },
  };

  return {surface, profiles, profilePINCurrent, closeProfilePIN};
}
