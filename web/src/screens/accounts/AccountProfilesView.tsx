import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {IdentityApi} from '@core/identity-client.ts';
import type {AccountEntryState} from '../../platform/account-entry';
import {AuthError, AuthStage, ServerProfiles, ServerProfilePIN} from '../../kit/accounts';
import {ProfilePINRecovery} from '../../kit/accounts/ProfilePINRecovery';
import {Button} from '../../kit/controls';

type RecoveryPhase = Parameters<typeof ProfilePINRecovery>[0]['phase'];
/** Structural view seam for the real Accounts-owned recovery controller.
 * No temporary proof, family or viewer credential is a view-model field. */
export type ProfileRecoveryView = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Readonly<{phase: RecoveryPhase; busy?: boolean; error?: unknown}>;
  password: (value: string) => Promise<void>;
  portico: (api: IdentityApi, origin: string, isCurrent: () => boolean) => Promise<void>;
  code: (value: string) => Promise<void>;
  reset: (value: string) => Promise<void>;
  openProfile: (pin: string, current: () => boolean) => Promise<void>;
  dispose: () => void;
}>;
export type ProfilePickerViewBinding = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => AccountEntryState;
  loadProfiles: () => Promise<void>;
  chooseProfile: (id: string, pin: string, remember: boolean) => Promise<void>;
  profileRecovery: (id: string, current: () => boolean) => ProfileRecoveryView;
  porticoRecovery: (flow: ProfileRecoveryView) => Promise<void>;
}>;

type ViewOwner = {retired: boolean; active?: {live: boolean}};
type Selection = Readonly<{id: string}>;
type RecoveryOpening = {flow: ProfileRecoveryView; binding: ProfilePickerViewBinding; owner: ViewOwner; flight: {live: boolean}; snapshot: DirectSnapshot; server: NonNullable<AccountEntryState['server']>; selection: Selection; active: boolean; completing: boolean};

/** auth13/14/18. The route supplies the actual captured entry/domain helpers,
 * retains their authority and owns their disposal. No renderer creates auth. */
export function AccountProfilesView({binding, onSwitchServer}: {binding: ProfilePickerViewBinding; onSwitchServer: () => void}) {
  const state = useSyncExternalStore(binding.subscribe, binding.getSnapshot);
  const [selection, setSelection] = useState<Selection>(), [remember, setRemember] = useState(false), [recovery, setRecovery] = useState<RecoveryOpening>();
  const [lock, setLock] = useState<Readonly<{error: unknown; until: number}>>(), [now, setNow] = useState(Date.now);
  const activeRecovery = useRef<RecoveryOpening | undefined>(undefined), selectedNow = useRef(selection);
  const owner = useMemo<ViewOwner>(() => { /* Preserve the original ownership/invalidation keys. */ void binding; void state.snapshot; void state.phase; void state.server; return ({retired: false}); }, [binding, state.snapshot, state.server, state.phase]);
  const rendered = useRef(owner), [flight, setFlight] = useState<{live: boolean}>();
  if (rendered.current !== owner) {rendered.current.retired = true; if (rendered.current.active) rendered.current.active.live = false; rendered.current = owner;}
  const retire = (opening: RecoveryOpening) => {
    opening.active = false; opening.flow.dispose();
    if (activeRecovery.current === opening) activeRecovery.current = undefined;
    setRecovery(current => current === opening ? undefined : current);
  };
  useLayoutEffect(() => {
    const own = {live: true}; owner.active = own; setFlight(own);
    selectedNow.current = undefined; setSelection(undefined); setRemember(false);
    return () => {
      own.live = false; if (owner.active === own) owner.active = undefined;
      const opening = activeRecovery.current;
      if (opening?.owner === owner && opening.flight === own) retire(opening);
    };
  }, [owner]);
  const viewCurrent = () => Boolean(flight?.live && !owner.retired && rendered.current === owner && owner.active === flight && binding.getSnapshot().snapshot === state.snapshot && binding.getSnapshot().server === state.server && binding.getSnapshot().phase === 'profiles');
  const openingCurrent = (opening: RecoveryOpening) => {
    const current = opening.binding.getSnapshot();
    return opening.active && activeRecovery.current === opening && !opening.owner.retired && rendered.current === opening.owner && opening.owner.active === opening.flight && opening.flight.live && selectedNow.current === opening.selection && current.phase === 'profiles' && current.snapshot === opening.snapshot && current.server === opening.server && current.snapshot.profiles.some(profile => profile.id === opening.selection.id);
  };
  useEffect(() => {
    const failure = state.error as {code?: unknown; retryAfterSeconds?: unknown} | undefined;
    const seconds = failure?.retryAfterSeconds;
    if (failure?.code !== 'profile_pin_locked' || typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) {setLock(undefined); return;}
    const start = Date.now(); setNow(start); setLock({error: state.error, until: start + seconds * 1000});
  }, [state.error]);
  useEffect(() => {
    if (!lock) return;
    const timer = setInterval(() => {const current = Date.now(); setNow(current); if (current >= lock.until) clearInterval(timer);}, 1000);
    return () => clearInterval(timer);
  }, [lock]);
  const snapshot = state.snapshot, server = state.server;
  // DirectSnapshot is this proved account's profile list. Library access is
  // evaluated inside each profile, never used to disable its picker tile.
  const profiles = snapshot?.profiles.map(profile => ({...profile, eligible: true}));
  const selected = profiles?.find(profile => profile.id === selection?.id);
  if (!snapshot || !server) return <AuthStage legal={false}>{!state.error && <p role="status">{defaultI18n.t('auth.loadingProfiles')}</p>}<AuthError error={state.error} />{Boolean(state.error) && <Button onClick={() => void binding.loadProfiles()}>{defaultI18n.t('action.tryAgain')}</Button>}</AuthStage>;
  // Required password/two-step pages are a different account-only continuation.
  // This picker cannot offer profiles until the actual controller clears it.
  if (state.phase !== 'profiles' || snapshot.twoStepSetupRequired) return null;
  const pinError = Boolean(state.error && ['profile_pin_invalid', 'profile_pin'].includes(String((state.error as {code?: unknown}).code)));
  const seconds = lock && lock.error === state.error ? Math.max(0, Math.ceil((lock.until - now) / 1000)) : 0;
  const otherProfiles = () => {
    if (!viewCurrent() || selectedNow.current !== selection || activeRecovery.current !== recovery) return;
    if (recovery) retire(recovery);
    selectedNow.current = undefined; setSelection(undefined); setRemember(false); void binding.loadProfiles();
  };
  const switchServer = () => {if (viewCurrent() && selectedNow.current === selection && activeRecovery.current === recovery) onSwitchServer();};

  return <>
    {selected ? <ServerProfilePIN key={JSON.stringify([selected.id, selected.pinRevision])} serverName={server.name} profile={selected} remember={remember} onRememberChange={value => {if (viewCurrent() && selectedNow.current === selection && !activeRecovery.current && !binding.getSnapshot().busy) setRemember(value);}}
      busy={state.busy || seconds > 0} error={pinError} rejectionKey={state.error} errorText={seconds ? defaultI18n.t('accounts.profiles.pinLocked', {seconds}) : undefined}
      failure={!pinError && !seconds ? state.error : undefined} onComplete={pin => {if (viewCurrent() && selectedNow.current === selection && !activeRecovery.current && !binding.getSnapshot().busy) void binding.chooseProfile(selected.id, pin, remember);}}
      onOtherProfiles={otherProfiles} onSwitchServer={switchServer} onForgotPIN={() => {
        if (!viewCurrent() || !flight || !selection || selectedNow.current !== selection || activeRecovery.current || binding.getSnapshot().busy) return;
        let opening: RecoveryOpening | undefined;
        const flow = binding.profileRecovery(selected.id, () => Boolean(opening && openingCurrent(opening)));
        opening = {flow, binding, owner, flight, snapshot, server, selection, active: true, completing: false};
        activeRecovery.current = opening; setRecovery(opening);
      }} /> : <ServerProfiles serverName={server.name} profiles={profiles!} error={state.error} onSwitchServer={switchServer}
        onSelect={profile => {if (!viewCurrent() || selectedNow.current !== selection || activeRecovery.current || binding.getSnapshot().busy) return; setRemember(false); if (profile.pinRequired) {const selected = {id: profile.id}; selectedNow.current = selected; setSelection(selected);} else void binding.chooseProfile(profile.id, '', false);}} />}
    {selected && recovery && <Recovery binding={binding} flow={recovery.flow} current={() => openingCurrent(recovery)} profileName={selected.name} username={snapshot.account.username}
      onClose={() => retire(recovery)} onOpen={pin => {
        if (!openingCurrent(recovery) || recovery.completing || recovery.flow.getSnapshot().phase !== 'done') return;
        recovery.completing = true; setRecovery(undefined);
        void recovery.flow.openProfile(pin, () => openingCurrent(recovery)).finally(() => retire(recovery)).catch(() => {});
      }} />}
  </>;
}

function Recovery({binding, flow, current, profileName, username, onClose, onOpen}: {
  binding: ProfilePickerViewBinding; flow: ProfileRecoveryView; current: () => boolean; profileName: string; username: DirectSnapshot['account']['username'];
  onClose: () => void; onOpen: (pin: string) => void;
}) {
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const active = useRef<{live: boolean} | undefined>(undefined), [lifetime, setLifetime] = useState<{live: boolean}>();
  useLayoutEffect(() => {const own = {live: true}; active.current = own; setLifetime(own); setBusy(false); return () => {own.live = false; if (active.current === own) active.current = undefined;};}, [flow]);
  const live = () => lifetime?.live && active.current === lifetime && current();
  const portico = async () => {
    if (busy || !live()) return; setBusy(true); setError(undefined);
    try {await binding.porticoRecovery(flow);} catch (failure) {if (live()) setError(failure);} finally {if (live()) setBusy(false);}
  };
  return <ProfilePINRecovery profileName={profileName} username={username} phase={state.phase} busy={state.busy || busy} error={state.error ?? error}
    onPassword={password => {if (live()) void flow.password(password);}} onPortico={() => void portico()} onCode={code => {if (live()) void flow.code(code);}} onPIN={pin => {if (live()) void flow.reset(pin);}} onClose={() => {if (live()) onClose();}} onOpen={pin => {if (live()) onOpen(pin);}} />;
}
