import React from 'react';
import {afterEntry} from '../../../../shell/after-entry';
import {ProfilePickerStage, ProfilePINStage} from '../../../../pages/shared/accounts/ProfileGateView';
import {profileFace} from '../../../../pages/shared/accounts/entry/entry-helpers';
import type {PINRecovery} from '../../../../pages/shared/accounts/entry/entry-types';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

/** Renders post-sign-in profile selection, profile PIN, TV forgot-PIN handoff, and touch PIN recovery stages via ProfileGateView. */
export function renderEntryProfileStage({view, props, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {onComplete} = props;
  const {
    theme, stage, setStage, busy, error, setError, username, code, setCode, wrongCodeTimer,
    server, profiles, profile, canRemember, remember, setRemember, profileFocus,
    pinRecovery, setPinRecovery, lifetime, operation, pinOpening, pinRecoveryRef, profileRef,
    retireRecovery, busyRef, stageRef, acting, stillPIN, openPIN, run, back, choose, backAction,
  } = view;
  const {status} = kit;

  if (pinRecovery) {
    const captured = pinRecovery;
    const current = () => lifetime.current && !captured.retired && pinRecoveryRef.current === captured
      && operation.current === captured.operation && profileRef.current === captured.profile && stageRef.current === 'profile-pin';
    return ProfilePINStage({
      tv: theme.isTV,
      code,
      onCodeChange: setCode,
      onSubmit: () => {},
      busy,
      rejected: !!error,
      onBack: back,
      onForgot: () => {},
      recovery: {
        controller: captured.controller,
        profileName: captured.profile.name,
        username,
        onOpen: value => {
          if (!current() || captured.controller.getSnapshot().phase !== 'done' || !/^\d{4}$/.test(value) || busyRef.current || acting.current) return;
          retireRecovery(captured); setCode(value);
          void run(async () => {await captured.operation.chooseProfile(captured.profile.id, value, canRemember && remember); if (lifetime.current && operation.current === captured.operation) onComplete();});
        },
        onClose: () => {if (!current()) return; retireRecovery(captured); setCode(''); setError(undefined);},
      },
    });
  }

  if (stage === 'profiles') {
    const originalOperation = operation.current!;
    const currentPicker = () => lifetime.current && operation.current === originalOperation && stageRef.current === 'profiles'
      && !busyRef.current && !acting.current;
    const selectProfile = (id: string) => {
      if (!currentPicker()) return;
      const selected = profiles.find(item => item.id === id);
      if (!selected) return;
      if (!theme.isTV) afterEntry.clear();
      if (selected.pinRequired) openPIN(selected, originalOperation); else void run(() => choose(id));
    };
    const main = theme.isTV ? undefined : profiles.find(value => value.primary);
    const openManagement = () => {
      if (!currentPicker() || !main) return;
      afterEntry.request({kind: 'profiles'}, main.id);
      if (main.pinRequired) openPIN(main, originalOperation); else void run(() => choose(main.id));
    };
    return ProfilePickerStage({
      tv: theme.isTV,
      testID: 'accounts-entry',
      serverName: server!.name,
      focusScope: `tv.auth.profiles.${server!.id}`,
      busy,
      profiles: profiles.map(value => ({id: value.id, name: value.name, ...profileFace(value), primary: value.primary, pinRequired: value.pinRequired, disabled: busy})),
      preferredProfileId: theme.isTV ? profileFocus.get(originalOperation) : undefined,
      onFocusChange: id => {if (id && operation.current === originalOperation) profileFocus.set(originalOperation, id);},
      onSelect: selectProfile,
      onManage: main ? openManagement : undefined,
      onAdd: main && profiles.length < 8 ? openManagement : undefined,
      canRemember,
      remember,
      onRememberChange: setRemember,
      rememberFocusId: 'accounts-remember-profile',
      status,
      waitTestID: 'accounts-profile-wait',
      onBack: backAction ? back : undefined,
    });
  }

  if (theme.isTV && stage === 'profile-forgot-pin') {
    return ProfilePINStage({
      tv: true,
      code,
      onCodeChange: setCode,
      onSubmit: () => {},
      busy,
      rejected: !!error,
      onBack: back,
      onForgot: () => {},
      forgot: {
        profileName: profile!.name,
        serverAddress: operation.current!.serverOrigin(),
        server: {id: server!.id, signIn: server!.signIn},
      },
    });
  }

  if (stage === 'profile-pin' && pinOpening.current) {
    const opening = pinOpening.current, {profile: target, operation: source} = opening;
    const allowed = () => stillPIN(opening) && !pinRecoveryRef.current && !busyRef.current && !acting.current;
    const changePIN = (value: string) => {
      if (!allowed()) return;
      // Once the viewer edits the boxes, the previous refusal must not clear the new input.
      if (wrongCodeTimer.current) {clearTimeout(wrongCodeTimer.current); wrongCodeTimer.current = undefined;}
      setCode(value);
    };
    const submitPIN = (value: string) => {
      if (!allowed()) return;
      void run(async () => {
        await source.chooseProfile(target.id, value, canRemember && remember);
        if (stillPIN(opening)) onComplete();
      });
    };
    const backPIN = () => {if (allowed()) back();};
    const onForgot = theme.isTV
      ? () => {
          if (!allowed()) return;
          pinOpening.current = undefined; stageRef.current = 'profile-forgot-pin';
          setCode(''); setError(undefined); setStage('profile-forgot-pin');
        }
      : () => {
          if (!allowed()) return;
          try {
            setError(undefined);
            const next: PINRecovery = {profile: target, operation: source, controller: source.profileRecovery(target.id), retired: false};
            pinOpening.current = {...opening};
            pinRecoveryRef.current = next; setPinRecovery(next);
          } catch (err) {setError(err);}
        };
    return ProfilePINStage({
      tv: theme.isTV,
      testID: 'accounts-entry',
      profile: {id: target.id, name: target.name, face: profileFace(target), primary: target.primary, pinRequired: true},
      code,
      onCodeChange: changePIN,
      onSubmit: submitPIN,
      busy,
      rejected: !!error,
      error: status,
      status,
      canRemember,
      remember,
      onRememberChange: setRemember,
      rememberFocusId: 'accounts-remember-profile',
      waitTestID: 'accounts-profile-wait',
      serverName: server?.name,
      onBack: backPIN,
      onForgot,
    });
  }

  return undefined;
}
