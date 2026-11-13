import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {AccountEntryState} from '../../platform/account-entry';
import {AuthError, AuthStage, ServerProfiles, ServerProfilePIN} from '../../kit/accounts';
import {ProfilePINRecovery, type PorticoRecoveryView} from '../../kit/accounts/ProfilePINRecovery';
import {goTo} from '../../platform/hosted-navigation';
import {porticoHandoffUrl} from '../../platform/portico-handoff';
import {browserAccountOrigin} from '../../platform/account-origin';
import {usePINLockSeconds} from '../../kit/accounts/pin-feedback';
import {Button} from '../../kit/controls';

type RecoveryPhase = Parameters<typeof ProfilePINRecovery>[0]['phase'];
/** Structural view seam for the real Accounts-owned recovery controller.
 * No temporary proof, family or viewer credential is a view-model field. */
export type ProfileRecoveryView = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Readonly<{phase: RecoveryPhase; busy?: boolean; error?: unknown; portico?: PorticoRecoveryView}>;
  password: (value: string) => Promise<void>;
  /** On a Portico Accounts server: reads the Portico Account this page holds (or that it holds none). */
  preparePortico: () => Promise<void>;
  porticoPassword: (password: string, twoStepCode: string) => Promise<void>;
  /** A fresh Google or Apple sign-in (a pop-up at web.getportico.tv); the code, when two-step is on, is asked after it returns. */
  porticoProvider?: (provider: 'google' | 'apple', twoStepCode: string | (() => Promise<string>)) => Promise<void>;
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
}>;

type ViewOwner = {retired: boolean; active?: {live: boolean}};
type Selection = Readonly<{id: string}>;
type RecoveryOpening = {flow: ProfileRecoveryView; binding: ProfilePickerViewBinding; owner: ViewOwner; flight: {live: boolean}; snapshot: DirectSnapshot; server: NonNullable<AccountEntryState['server']>; selection: Selection; active: boolean; completing: boolean};

/** auth13/14/18. The route supplies the actual captured entry/domain helpers,
 * retains their authority and owns their disposal. No renderer creates auth. */
export function AccountProfilesView({binding, onSwitchServer, focusProfileId}: {binding: ProfilePickerViewBinding; onSwitchServer: () => void; focusProfileId?: string}) {
  const state = useSyncExternalStore(binding.subscribe, binding.getSnapshot);
  const [selection, setSelection] = useState<Selection>(), [remember, setRemember] = useState(false), [recovery, setRecovery] = useState<RecoveryOpening>();
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
  const seconds = usePINLockSeconds(state.error);
  const snapshot = state.snapshot, server = state.server;
  // DirectSnapshot is this proved account's profile list. Library access is
  // evaluated inside each profile, never used to disable its picker tile.
  const profiles = snapshot?.profiles.map(profile => ({...profile, eligible: true}));
  const selected = profiles?.find(profile => profile.id === selection?.id);
  // Offered only where the server's Remember PIN for isn't Off (the server would not remember).
  const canRemember = snapshot?.rememberProfiles === true;
  if (!snapshot || !server) return <AuthStage legal={false}>{!state.error && <p role="status">{defaultI18n.t('auth.loadingProfiles')}</p>}<AuthError error={state.error} />{Boolean(state.error) && <Button onClick={() => void binding.loadProfiles()}>{defaultI18n.t('action.tryAgain')}</Button>}</AuthStage>;
  // Required password/two-step pages are a different account-only continuation.
  // This picker cannot offer profiles until the actual controller clears it.
  if (state.phase !== 'profiles' || snapshot.twoStepSetupRequired) return null;
  const pinError = Boolean(state.error && ['profile_pin_invalid', 'profile_pin'].includes(String((state.error as {code?: unknown}).code)));
  const otherProfiles = () => {
    if (!viewCurrent() || selectedNow.current !== selection || activeRecovery.current !== recovery) return;
    if (recovery) retire(recovery);
    selectedNow.current = undefined; setSelection(undefined); void binding.loadProfiles();
  };
  const switchServer = () => {if (viewCurrent() && selectedNow.current === selection && activeRecovery.current === recovery) onSwitchServer();};

  return <>
    {selected ? <ServerProfilePIN key={JSON.stringify([selected.id, selected.pinRequired])} serverName={server.name} profile={selected} remember={remember} onRememberChange={!canRemember ? undefined : value => {if (viewCurrent() && selectedNow.current === selection && !activeRecovery.current && !binding.getSnapshot().busy) setRemember(value);}}
      busy={state.busy || seconds > 0} error={pinError} rejectionKey={state.error} errorText={seconds ? defaultI18n.t('accounts.profiles.pinLocked', {seconds}) : undefined}
      failure={!pinError && !seconds ? state.error : undefined} onComplete={pin => {if (viewCurrent() && selectedNow.current === selection && !activeRecovery.current && !binding.getSnapshot().busy) void binding.chooseProfile(selected.id, pin, canRemember && remember);}}
      onOtherProfiles={otherProfiles} onSwitchServer={switchServer} onForgotPIN={() => {
        if (!viewCurrent() || !flight || !selection || selectedNow.current !== selection || activeRecovery.current || binding.getSnapshot().busy) return;
        let opening: RecoveryOpening | undefined;
        const flow = binding.profileRecovery(selected.id, () => Boolean(opening && openingCurrent(opening)));
        opening = {flow, binding, owner, flight, snapshot, server, selection, active: true, completing: false};
        activeRecovery.current = opening; setRecovery(opening);
      }} /> : <ServerProfiles serverName={server.name} profiles={profiles!} error={state.error} onSwitchServer={switchServer} autoFocusId={focusProfileId}
        remember={remember} onRememberChange={!canRemember ? undefined : value => {if (viewCurrent() && !activeRecovery.current && !binding.getSnapshot().busy) setRemember(value);}}
        // A tick on the picker remembers a profile without a PIN at once, and carries to a PIN profile's page, where it applies after a correct PIN.
        onSelect={profile => {if (!viewCurrent() || selectedNow.current !== selection || activeRecovery.current || binding.getSnapshot().busy) return; if (profile.pinRequired) {const selected = {id: profile.id}; selectedNow.current = selected; setSelection(selected);} else void binding.chooseProfile(profile.id, '', canRemember && remember);}} />}
    {selected && recovery && <Recovery binding={binding} flow={recovery.flow} current={() => openingCurrent(recovery)} profileName={selected.name} username={snapshot.account.username} serverId={server.id}
      onClose={() => retire(recovery)} onOpen={pin => {
        if (!openingCurrent(recovery) || recovery.completing || recovery.flow.getSnapshot().phase !== 'done') return;
        recovery.completing = true; setRecovery(undefined);
        void recovery.flow.openProfile(pin, () => openingCurrent(recovery)).finally(() => retire(recovery)).catch(() => {});
      }} />}
  </>;
}

function Recovery({binding, flow, current, profileName, username, serverId, onClose, onOpen}: {
  binding: ProfilePickerViewBinding; flow: ProfileRecoveryView; current: () => boolean; profileName: string; username: DirectSnapshot['account']['username'];
  serverId: string; onClose: () => void; onOpen: (pin: string) => void;
}) {
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot);
  const active = useRef<{live: boolean} | undefined>(undefined), [lifetime, setLifetime] = useState<{live: boolean}>();
  useLayoutEffect(() => {const own = {live: true}; active.current = own; setLifetime(own); return () => {own.live = false; if (active.current === own) active.current = undefined;};}, [flow]);
  const live = () => lifetime?.live && active.current === lifetime && current();
  // On a Portico Accounts server the Portico Account this page holds is read as the step opens.
  const porticoStep = state.phase === 'portico' && !state.portico;
  useLayoutEffect(() => {if (porticoStep) void flow.preparePortico();}, [flow, porticoStep]);
  const [launched, setLaunched] = useState<'google' | 'apple'>(), [codeAsk, setCodeAsk] = useState<(code: string) => void>();
  const startProvider = (provider: 'google' | 'apple') => {
    if (!live() || !flow.porticoProvider || state.portico?.phase !== 'ready') return;
    setLaunched(provider);
    const code = state.portico.account.twoStep ? () => new Promise<string>(resolve => setCodeAsk(() => resolve)) : '';
    void flow.porticoProvider(provider, code);
  };
  return <ProfilePINRecovery profileName={profileName} username={username} phase={state.phase} busy={state.busy} error={state.error} portico={state.portico}
    provider={{launched, askingCode: !!codeAsk}} onPorticoProvider={flow.porticoProvider ? startProvider : undefined}
    onProviderCode={code => {const answer = codeAsk; setCodeAsk(undefined); answer?.(code);}}
    onPassword={password => {if (live()) void flow.password(password);}} onPorticoPassword={(password, code) => {if (live()) void flow.porticoPassword(password, code);}}
    onPorticoRetry={() => {if (live()) void flow.preparePortico();}} onSetPassword={() => {if (live()) goTo('/account?tab=security');}}
    onPorticoElsewhere={() => {if (live()) location.assign(porticoHandoffUrl({hostedOrigin: browserAccountOrigin(import.meta.env, location), serverId, pageOrigin: location.origin}).url);}}
    onCode={code => {if (live()) void flow.code(code);}} onPIN={pin => {if (live()) void flow.reset(pin);}} onClose={() => {if (live()) onClose();}} onOpen={pin => {if (live()) onOpen(pin);}} />;
}
