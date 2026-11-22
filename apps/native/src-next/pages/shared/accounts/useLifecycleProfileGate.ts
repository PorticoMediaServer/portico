import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {HttpHostedApi} from '../../../../../../packages/client-core/src';
import type {PickerProfile} from '../../../../../../packages/client-core/src/session-lifecycle';
import {useTheme} from '../../../kit/theme';
import {nativeSessionLifecycle} from '../../../platform/native-session-lifecycle';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../../platform/profile-recovery';
import {nativeAccounts} from '../../../platform/accounts';
import {hostedUrl} from '../../../platform/account-authority';
import {nativeNetworkFetch} from '../../../platform/server-connections';
import {afterEntry} from '../../../shell/after-entry';
import {profilePinFailureText} from '../../../screens/accounts/profile-pin-failure';
import {useTVMenuCapture} from '../../../platform/tv-menu';
import {registerBack} from '../../../kit/navigation/back';

/**
 * Who's watching, and a profile's PIN, as the session lifecycle asks for them: at launch when no profile is remembered,
 * and when a PIN changed elsewhere (profile_pin_reentry). A callback belongs to the committed prompt and the exact
 * lifecycle state that supplied it. The tick remembers a profile without a PIN at once, and carries to a PIN profile's
 * page, where the server applies it only after a correct PIN: nothing is sent before. Moved from the old gate as it was.
 */
export function useLifecycleProfileGate() {
  const tv = useTheme().isTV;
  const {lifecycle, ports} = nativeSessionLifecycle();
  const snapshot = useSyncExternalStore(lifecycle.subscribe, lifecycle.getSnapshot, lifecycle.getSnapshot);
  const pick = useSyncExternalStore(lifecycle.actions.pickProfile.subscribe, lifecycle.actions.pickProfile.getSnapshot, lifecycle.actions.pickProfile.getSnapshot);
  const submit = useSyncExternalStore(lifecycle.actions.submitPin.subscribe, lifecycle.actions.submitPin.getSnapshot, lifecycle.actions.submitPin.getSnapshot);
  const [code, setCode] = useState(''), [remember, setRemember] = useState(true), [forgot, setForgot] = useState(false);
  const [recovery, setRecovery] = useState<{controller: NativeProfileRecovery; id: string; name: string}>();
  const state = snapshot.state;
  const presented = useMemo(() => ({state, forgot, recovery}), [state, forgot, recovery]);
  const committed = useRef<typeof presented | undefined>(undefined);
  useLayoutEffect(() => {
    committed.current = presented;
    return () => {committed.current = undefined;};
  }, [presented]);
  const currentPIN = () => committed.current === presented && lifecycle.getSnapshot().state === state;
  const profileId = state.kind === 'enteringPin' ? state.profile.id : undefined;
  // A new PIN screen starts empty; the tick is the person's and carries from the picker to the PIN page and back.
  useEffect(() => {setCode(''); setForgot(false);}, [profileId]);
  // TV, notes/motion.md S.3, as the in-app picker does: a refused PIN stays in the red boxes for 400 ms, then they empty
  // for the next try (each refusal is a new lifecycle state). Only the refused code is emptied: a digit pressed in that
  // time has already started the next one. The phone's field keeps what is typed.
  const refused = tv && state.kind === 'enteringPin' && state.rejected ? state : undefined;
  const codeNow = useRef(code); codeNow.current = code;
  useEffect(() => {
    if (!refused) return;
    const refusedCode = codeNow.current;
    const timer = setTimeout(() => {
      if (lifecycle.getSnapshot().state === refused) setCode(current => current === refusedCode ? '' : current);
    }, 400);
    return () => clearTimeout(timer);
  }, [lifecycle, refused]);
  const choosing = state.kind === 'choosingProfile' || state.kind === 'enteringPin';
  useEffect(() => {if (!choosing) setRemember(true);}, [choosing]);
  useEffect(() => () => recovery?.controller.dispose(), [recovery]);
  const back = () => {
    if (state.kind === 'enteringPin' && !currentPIN()) return;
    if (state.kind === 'enteringPin') committed.current = undefined;
    afterEntry.clear();
    if (recovery) {recovery.controller.dispose(); setRecovery(undefined); return;}
    if (forgot) {setForgot(false); return;}
    // Leaving the PIN page forgets its refusal, so a PIN page opened again starts with no message.
    if (state.kind === 'enteringPin') {setCode(''); lifecycle.actions.submitPin.reset(); lifecycle.otherProfiles();}
    else if (state.kind === 'choosingProfile' && state.switching) lifecycle.cancelSwitch();
  };
  const handlesBack = state.kind === 'enteringPin' || !!recovery || forgot || state.kind === 'choosingProfile' && state.switching;
  useTVMenuCapture(tv && handlesBack);
  useEffect(() => {
    const subscription = registerBack(() => {back(); return handlesBack;}, {scope: 'surface'});
    return () => subscription();
  });
  if (state.kind !== 'choosingProfile' && state.kind !== 'enteringPin') return undefined;

  const connection = ports.connection(state.server.serverId);
  const busy = pick.phase === 'working' || submit.phase === 'working';
  const art = (id: string) => connection?.account.profiles.find(value => value.id === id)?.art ?? 'blue';
  const option = (value: PickerProfile) => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: art(value.id),
    primary: value.primary, pinRequired: value.pinRequired, disabled: busy});
  // Each screen says only its own failure: a refused PIN stays on the PIN page and doesn't follow Back to the picker,
  // and the picker only says why a pick failed.
  const failed = state.kind === 'enteringPin' ? submit.phase === 'failed' ? submit.failure.cause : undefined
    : pick.phase === 'failed' ? pick.failure.cause : undefined;
  const failure = failed ? presentError(failed, state.kind === 'enteringPin' ? 'profiles' : 'sign-in') : undefined;
  const failureText = failure && !failure.silent ? profilePinFailureText(failed) ?? failure.body : undefined;
  const common = {tv, busy, back, remember, setRemember, option, art, failureText, server: state.server, connection};

  if (state.kind === 'choosingProfile') {
    // Add profile and Manage profiles open Profiles from the main profile, after its PIN if it has one (rules 25 and 27).
    const main = tv ? undefined : state.profiles.find(value => value.primary);
    const openManagement = () => {if (busy || !main) return; afterEntry.request({kind: 'profiles'}, main.id); lifecycle.actions.pickProfile.start(main.id, remember && state.canRemember);};
    return {...common, kind: 'choosing' as const, state, management: main ? openManagement : undefined,
      select: (id: string) => {afterEntry.clear(); lifecycle.actions.pickProfile.start(id, remember && state.canRemember);}};
  }

  const profile = state.profile;
  const ready = () => currentPIN() && lifecycle.actions.pickProfile.getSnapshot().phase !== 'working'
    && lifecycle.actions.submitPin.getSnapshot().phase !== 'working';
  const send = (value: string) => {
    if (/^\d{4}$/.test(value) && ready()) lifecycle.actions.submitPin.start(value, remember && state.canRemember);
  };
  const openRecovery = () => {
    if (!connection || !ready()) return;
    const session = connection.session, pin = connection.api.getRouteConnection()?.pin;
    if (!pin) return;
    const authorityCurrent = () => {
      const live = connection.api.getRouteConnection()?.pin, now = lifecycle.getSnapshot().state;
      return (now.kind === 'enteringPin' || now.kind === 'choosingProfile') && now.viewer.familyId === session.sessionFamilyId
        && !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
    };
    const next = {id: profile.id, name: profile.name, controller: new NativeProfileRecovery({api: connection.api, snapshot: connection.account, profileId: profile.id,
      selectedFamilyId: session.sessionFamilyId, kind: state.server.signIn, authorityCurrent,
      temporary: grant => temporaryRecoveryApi(connection.api, grant),
      portico: async () => {
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
        const account = await credentials.accessSession();
        if (!account) throw new Error('Sign in to your Portico Account first.');
        return {hosted: new HttpHostedApi(hostedUrl, account.accessToken, nativeNetworkFetch), origin: hostedUrl,
          accountCurrent: () => {const live = credentials.getSnapshot().session; return !!live && live.account.id === account.account.id && live.familyId === account.familyId;}};
      }})};
    committed.current = undefined; setRecovery(next);
  };
  return {...common, kind: 'pin' as const, state, profile, code, forgot, recovery, openRecovery, send,
    face: {initial: [...profile.name][0] ?? '', color: art(profile.id)},
    changeCode: (value: string) => {if (ready()) setCode(value);},
    changeRemember: (value: boolean) => {if (ready()) setRemember(value);},
    startForgot: () => {if (ready()) {committed.current = undefined; setCode(''); setForgot(true);}},
    recoveryOpen: (value: string) => {
      if (!ready() || !recovery) return;
      send(value); committed.current = undefined;
      recovery.controller.dispose(); setRecovery(undefined); setCode(value);
    },
    recoveryClose: () => {
      if (!currentPIN() || !recovery) return;
      committed.current = undefined; recovery.controller.dispose(); setRecovery(undefined); setCode('');
    }};
}
