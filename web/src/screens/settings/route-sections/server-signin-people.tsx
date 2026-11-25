import {defaultI18n} from '@i18n';
import {createDirectMember, type NewDirectMember} from '@core/direct-member-create.ts';
import {uploadOwnerMemberAvatar} from '@core/owner-upload-requests.ts';
import {peopleStreamLimits} from '@core/presentation/settings-bindings.ts';
import {SettingsPeopleClient} from '@core/settings-people.ts';
import {Icon} from '../../../kit/Icon';
import {router} from '../../../router';
import {subscribeFacts} from '../../../shell/fact-source';
import {browserAccountOrigin} from '../../../platform/account-origin';
import {browserAppSessions} from '../../../platform/server-connections';
import serverStyles from '../server/server.module.css';
import {SettingsStorageSource} from '../server/storage-source';
import {StorageDataFolder} from '../server/StorageUsage';
import {PeopleScreen, type PeopleBindings} from '../People';
import {createPeopleDevicesApi} from '../people-devices-api';
import {createPeopleMemberProfilesApi} from '../people-member-profiles-api';
import {SignInSettingsSource} from '../signin-source';
import {LocalOwnerTransferSource} from '../local-owner-transfer';
import {MemberResetCodeSource} from '../member-reset-code';
import {localOwnerTransferTransport} from '../settings-request';
import {SettingsOwnerSwitch} from '../owner-switch';
import {OwnerSignInSwitch} from '../OwnerSignInSwitch';
import {LocalMoveSwitch} from '../LocalMoveSwitch';
import {MoveCodesHost} from '../MoveCodesHost';
import {MoveInProgress, SignInMoveWatch} from '../MoveInProgress';
import {MoveCodesPortProvider, MoveCodeRowAction} from '../move-codes-port';
import {ChangeOwner} from '../ChangeOwner';
import {LocalChangeOwnerRow} from '../LocalChangeOwnerRow';
import {SettingsInvitations} from '../invitations';
import {activeServerSection, canManageServerSection, type ServerSectionGateInput} from './access-gate';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

export type ServerSigninPeopleHandle = Readonly<{
  surface: SettingsSectionSurface;
  signIn?: SignInSettingsSource;
}>;

/** Server › Sign-in and Server › People: owner sign-in switch, local owner transfer, reset codes, and member administration. */
export function createServerSigninPeopleSection(
  env: SettingsRouteEnv,
  gate: ServerSectionGateInput,
  retireTransferredContext: () => void,
): ServerSigninPeopleHandle {
  const {api, originalApi, context, buildContext, route, role, identity, viewer, session, current, assert, signal, reportError, reportActionError, openHome, openSettingsRow, inputActivity, authorityListeners} = env;
  const transferOrigin = originalApi.baseUrl, transferRoute = originalApi.getRouteConnection();
  const transferPrincipal = Object.freeze({...viewer, family: session.sessionFamilyId});
  // Independent of this Settings build or dialog. After a known write, only this original shell identity/family may reconcile its actual authority.
  const originalTransferAuthority = () => {
    try {
      if (!context.isCurrent() || !transferRoute || transferRoute.getSnapshot().phase !== 'ready'
        || originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute
        || context.facts.identity.signIn !== 'local') return false;
      const now = context.session();
      return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === transferPrincipal.serverId
        && now.viewer.authority === transferPrincipal.authority && now.viewer.accountId === transferPrincipal.accountId
        && now.viewer.profileId === transferPrincipal.profileId;
    } catch {return false;}
  };

  let localOwnerTransfer: LocalOwnerTransferSource | undefined;
  const ownerSwitch = canManageServerSection('signin', gate)
    ? new SettingsOwnerSwitch(api, {
      serverId: viewer.serverId,
      isCurrent: () => current() && !localOwnerTransfer?.inFlight && context.session().viewer.role === 'owner'
        && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageSignIn === true
        && context.facts.identity.signIn === identity.signIn,
      hostedOrigin: browserAccountOrigin(import.meta.env, globalThis.location),
      selectionRevision: async () => (await browserAppSessions().read()).revision,
      signIn: identity.signIn as 'local' | 'portico',
      serverName: () => context.facts.identity.serverName,
    }) : undefined;

  const localOwnerCurrent = () => {
    if (!current() || route.scope !== 'server' || route.section !== 'signin' || identity.signIn !== 'local' || viewer.authority !== 'local'
      || !transferRoute || transferRoute.pin.serverId !== transferPrincipal.serverId || transferRoute.getSnapshot().phase !== 'ready'
      || originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute
      || ownerSwitch?.getMove() || ownerSwitch?.getSnapshot().busy) return false;
    const now = context.session(), facts = context.facts.identity;
    return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === transferPrincipal.serverId
      && now.viewer.authority === transferPrincipal.authority && now.viewer.accountId === transferPrincipal.accountId
      && now.viewer.profileId === transferPrincipal.profileId && now.viewer.role === 'owner'
      && facts.primaryProfile === true && facts.signIn === 'local' && facts.capabilities?.manageAccount === true
      && facts.capabilities?.managePeople === true && facts.capabilities?.manageSignIn === true;
  };

  localOwnerTransfer = localOwnerCurrent() ? new LocalOwnerTransferSource(api, {
    accountId: viewer.accountId,
    profileId: viewer.profileId,
    isCurrent: localOwnerCurrent,
    transferApi: localOwnerTransferTransport(originalApi, () => {if (!localOwnerCurrent()) throw new DOMException('Settings build retired', 'AbortError');}, signal),
    onTransferred: async publish => {
      // `publish` was checked against the exact opening at acknowledgement; capture location before retirement.
      const location = publish ? router.state.location : undefined;
      retireTransferredContext();
      if (!originalTransferAuthority()) return;
      // No latest context, auth object or location may be adopted here.
      const reconcile = context.auth.permissionDenied();
      if (location && router.state.location === location) void router.navigate({to: '/', replace: true}).catch(() => {});
      await reconcile;
    },
  }) : undefined;

  const signIn = canManageServerSection('signin', gate)
    ? new SignInSettingsSource(api, {
      serverId: viewer.serverId,
      signIn: identity.signIn as 'local' | 'portico',
      isCurrent: current,
      localOwnerTransfer,
      capabilities: () => ({
        manageAccount: identity.capabilities?.manageAccount === true,
        managePeople: identity.capabilities?.managePeople === true,
        manageServer: identity.capabilities?.manageServer === true,
        manageSignIn: current() && identity.capabilities?.manageSignIn === true,
      }),
      onAuthorityChanged: () => {if (current()) void router.navigate({to: '/sign-in', replace: true});},
      onPinManage: reportError,
      ownerSwitch,
    }) : undefined;

  // The Sign-in page's Reset file group names the server's own data folder (same bounded storage report).
  const signinStorage = route.section === 'signin' && canManageServerSection('storage', gate)
    ? new SettingsStorageSource(api, current, reportError) : undefined;

  const memberResetPin = transferRoute?.pin.publicKey;
  const memberResetCurrent = () => {
    if (!current() || route.scope !== 'server' || route.section !== 'people' || identity.signIn !== 'local' || viewer.authority !== 'local'
      || originalApi.baseUrl !== transferOrigin || originalApi.getRouteConnection() !== transferRoute
      || !transferRoute || !memberResetPin || transferRoute.pin.publicKey !== memberResetPin
      || transferRoute.pin.serverId !== viewer.serverId || transferRoute.getSnapshot().phase !== 'ready') return false;
    const now = context.session(), facts = context.facts.identity;
    return now.sessionFamilyId === transferPrincipal.family && now.viewer.serverId === viewer.serverId
      && now.viewer.authority === viewer.authority && now.viewer.accountId === viewer.accountId
      && now.viewer.profileId === viewer.profileId && now.viewer.role === 'owner'
      && facts.primaryProfile === true && facts.signIn === 'local'
      && facts.capabilities?.managePeople === true && facts.capabilities?.manageAccount === true;
  };
  const memberResetCodes = memberResetCurrent() ? new MemberResetCodeSource({
    request: async <T,>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> => {
      if (!memberResetCurrent()) throw new DOMException('Settings reset authority retired', 'AbortError');
      const value = await api.request<T>(path, method, body, input);
      if (!memberResetCurrent()) throw new DOMException('Settings reset authority retired', 'AbortError');
      return value;
    },
  }, {
    serverId: viewer.serverId,
    accountId: viewer.accountId,
    isCurrent: memberResetCurrent,
    copy: async code => {
      assert();
      if (!globalThis.navigator?.clipboard?.writeText) throw new Error('Clipboard unavailable.');
      await navigator.clipboard.writeText(code);
    },
  }) : undefined;

  const people: PeopleBindings | undefined = activeServerSection(route, 'people', gate) ? {
    inputActivity,
    client: new SettingsPeopleClient(api, current, () => buildContext.facts.identity.libraries),
    devices: createPeopleDevicesApi(api, current),
    memberProfiles: createPeopleMemberProfilesApi(api, current, async (path, body, contentType) => {
      assert();
      const response = await uploadOwnerMemberAvatar(api, path, body, contentType);
      if (!response.ok) throw Object.assign(new Error('Profile picture could not be saved.'), {status: response.status});
      return response.json();
    }, context.artwork),
    get serverName() {return buildContext.facts.serverName;},
    get libraries() {return buildContext.facts.identity.libraries;},
    subscribe: listener => subscribeFacts(buildContext, listener),
    memberResetCodes,
    // Only the owner may read and set stream limits on the rebuilt server.
    actor: {accountId: viewer.accountId, role: role as 'owner' | 'admin'},
    ...(role === 'owner' ? {streamLimits: peopleStreamLimits} : {}),
    invitations: new SettingsInvitations(api, current, api.baseUrl, viewer.serverId),
    signIn: identity.signIn,
    openHome,
    reportError: reportActionError,
    // owner-people 6/25: only the owner of a local-sign-in server adds accounts here (the server refuses anyone else).
    ...(role === 'owner' && identity.signIn === 'local' ? {addAccount: (input: NewDirectMember, signal: AbortSignal) => {assert(); return createDirectMember(api, input, signal);}} : {}),
    authority: {
      scope: {serverId: viewer.serverId, authority: viewer.authority as 'local' | 'hosted', accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: session.sessionFamilyId},
      isCurrent: current,
      subscribe: listener => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
      onAuthorityChanged: openHome,
    },
  } : undefined;

  const surface: SettingsSectionSurface = {
    eligible: (row, selected, controller) => {
      if (row.id === 'signin.people') return current() && (role === 'owner' || role === 'admin') && controller.sectionVisible(selected.section);
      if (row.id === 'signin.outside') return current() && role === 'owner' && controller.sectionVisible(selected.section);
      if (row.id === 'signin.dataFolder') return !!signinStorage && current() && controller.sectionVisible(selected.section);
      if (row.id === 'people.table') return !!people && current() && controller.sectionVisible(selected.section);
      return undefined;
    },
    rowState: (row, selected, controller) => {
      if (row.id === 'signin.people') return {present: current() && (role === 'owner' || role === 'admin') && controller.sectionVisible(selected.section)};
      if (row.id === 'signin.outside') return {present: current() && role === 'owner' && controller.sectionVisible(selected.section)};
      if (row.id === 'signin.dataFolder') return {present: !!signinStorage && current() && controller.sectionVisible(selected.section)};
      if (row.id === 'people.table') return {present: !!people && current() && controller.sectionVisible(selected.section)};
      return undefined;
    },
    special: (row, state) => {
      if (row.id === 'signin.people') {
        return <button type="button" className={serverStyles.sectionLink} disabled={state.disabled} onClick={() => {if (current()) openSettingsRow({scope: 'server', section: 'people'});}}>{defaultI18n.t(row.control.label ?? row.label)}<Icon name="i-right" size={16} /></button>;
      }
      if (row.id === 'signin.outside') {
        return <button type="button" className={serverStyles.sectionLink} disabled={state.disabled} onClick={() => {if (current()) openSettingsRow({scope: 'server', section: 'remote'});}}>{defaultI18n.t(row.control.label ?? row.label)}<Icon name="i-right" size={16} /></button>;
      }
      if (row.id === 'signin.dataFolder' && signinStorage) return <StorageDataFolder source={signinStorage} label={defaultI18n.t(row.label)} />;
      if (['signin.switchToPortico', 'signin.startOverPortico'].includes(row.id) && signIn?.ownerSwitch) {
        return <OwnerSignInSwitch source={signIn.ownerSwitch} startOver={row.id === 'signin.startOverPortico'} isCurrent={current} onError={reportActionError} />;
      }
      if (['signin.switchToLocal', 'signin.startOverLocal'].includes(row.id) && signIn?.ownerSwitch) {
        return <LocalMoveSwitch source={signIn.ownerSwitch} startOver={row.id === 'signin.startOverLocal'} isCurrent={current} onError={reportActionError} />;
      }
      if (row.id === 'signin.changeOwner' && signIn?.ownerSwitch) return <ChangeOwner source={signIn.ownerSwitch} isCurrent={current} onError={reportActionError} />;
      if (row.id === 'signin.changeOwnerLocal' && signIn?.localOwnerTransfer) return <LocalChangeOwnerRow source={signIn.localOwnerTransfer} isCurrent={current} onError={reportActionError} />;
      return undefined;
    },
    section: (selected, _openPanel, renderRows) => {
      if (selected.section.scope === 'server' && selected.section.id === 'signin') {
        return <SignInMoveWatch source={signIn?.ownerSwitch}>{move => <>
          {move && signIn?.ownerSwitch ? <MoveInProgress move={move} source={signIn.ownerSwitch} isCurrent={current}
            onPeople={() => {if (current()) openSettingsRow({scope: 'server', section: 'people'});}} onError={reportActionError} /> : null}
          {renderRows(selected.section)}
        </>}</SignInMoveWatch>;
      }
      if (selected.section.id === 'people' && people) {
        return <MoveCodesPortProvider>
          {signIn?.ownerSwitch && <MoveCodesHost source={signIn.ownerSwitch} serverName={buildContext.facts.identity.serverName} isCurrent={current} onError={reportActionError} />}
          <SignInMoveWatch source={signIn?.ownerSwitch}>{move => <PeopleScreen bindings={people}
            advancedRows={selected.section.advanced ? renderRows({advanced: selected.section.advanced}) : undefined}
            initialStatus={move ? 'waiting' : undefined}
            rowActions={person => person.status === 'waiting' ? <MoveCodeRowAction accountId={person.accountId} hasCode={person.hasMoveCode} /> : null} />}</SignInMoveWatch>
        </MoveCodesPortProvider>;
      }
      return undefined;
    },
    quietInputs: () => signIn && route.section === 'signin' ? [{
      id: 'signin',
      refresh: signIn.refresh,
      cancel: signIn.invalidateQuiet,
      subscribe: signIn.subscribe,
      settlement: () => signIn.getSnapshot().document,
    }] : [],
    dispose: () => {
      signinStorage?.dispose();
      signIn?.dispose();
      memberResetCodes?.dispose();
      people?.invitations?.dispose();
      people?.client.dispose();
    },
  };

  return {surface, signIn};
}
