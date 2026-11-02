import {useLayoutEffect, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {defineWebScreen} from '../../route-registry';
import {router} from '../../router';
import {profileSettingsDestination} from '../../route-registry-destination';
import {hasTwoStepSetup} from '../../platform/two-step-continuation';
import {createBrowserAccountEntry, type BrowserAccountEntry} from '../../platform/account-entry';
import {browserAccount} from '../../platform/account';
import {AuthError, AuthStage} from '../../kit/accounts';
import {Button} from '../../kit/controls';
import {AccountIdentityFailure} from './AccountIdentityFailure';
import {currentServedServerOrigin, forgetDestination} from '../../platform/served-origin';
import {AccountProfilesView, type ProfilePickerViewBinding} from './AccountProfilesView';
import {openAfterProfile} from './open-after-profile';

function ProfilesScreen() {
  const [entry, setEntry] = useState<BrowserAccountEntry>();
  useLayoutEffect(() => {
    const current = createBrowserAccountEntry(); setEntry(current);
    void current.loadProfiles();
    return () => current.dispose();
  }, []);
  return entry ? <BoundProfiles entry={entry} /> : null;
}
function BoundProfiles({entry}: {entry: BrowserAccountEntry}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot), [epoch, setEpoch] = useState(0), delivered = useRef(false);
  // The profile this tab had open, when the launch gate sent it here.
  const asked: unknown = useSearch({from: '/profiles'}).profileId, focus = typeof asked === 'string' ? asked : undefined;
  useEffect(() => entry.subscribeAuthorityRetired(() => {setEpoch(value => value + 1);}), [entry]);
  useEffect(() => {
    if (entry.getSnapshot() !== state) return;
    if (state.phase === 'ready' && !delivered.current) {delivered.current = true;void openAfterProfile(!!(state.server && state.snapshot && hasTwoStepSetup(state.server.id, state.snapshot.account.id)));}
    // A restored restricted account has no submitted password in this page.
    // Re-enter the existing sign-in, never invent an enrollment proof field.
    if (state.phase === 'two-step-setup' || state.phase === 'password-change') void router.navigate({to: '/sign-in', replace: true});
  }, [entry, state]);
  const binding = useMemo<ProfilePickerViewBinding>(() => ({
    subscribe: entry.subscribe, getSnapshot: entry.getSnapshot,
    loadProfiles: () => entry.loadProfiles(), chooseProfile: (id, pin, remember) => entry.chooseProfile(id, pin, remember),
    profileRecovery: (id, current) => entry.profileRecovery(id, current),
    async porticoRecovery(flow) {
      const account = browserAccount(); await account.service.synchronize();
      const session = await account.service.accessSession();
      const current = () => {const selected = account.service.getSnapshot().session;return selected?.account.id === session.account.id && selected.familyId === session.familyId;};
      if (!current()) return;
      await flow.portico({request: async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => {
        if (!current()) throw new DOMException('Account changed', 'AbortError');
        const response = await account.api.send<T>(path, method, body, signal, session.accessToken);
        if (!current()) throw new DOMException('Account changed', 'AbortError');
        return response;
      }}, account.api.origin, current);
    },
  }), [entry]);
  const switchServer = () => {void router.navigate({to: '/servers'});};
  const identityFailure = entry.serverIdentityFailure(switchServer, () => {
    void router.navigate(forgetDestination(currentServedServerOrigin()).to === 'sign-in' ? {to: '/sign-in', replace: true} : {to: '/servers'});
  });
  if (identityFailure) return <AccountIdentityFailure binding={identityFailure}/>;
  if (state.error && !state.snapshot) return <AuthStage legal={false}><AuthError error={state.error} />
    <Button onClick={() => void entry.loadProfiles()}>{defaultI18n.t('action.tryAgain')}</Button>
    <Button onClick={switchServer}>{defaultI18n.t('settings.switchServer')}</Button></AuthStage>;
  return <AccountProfilesView key={epoch} binding={binding} onSwitchServer={switchServer} focusProfileId={focus} />;
}
export default defineWebScreen({id: 'profiles', component: ProfilesScreen, chrome: 'standalone'});
