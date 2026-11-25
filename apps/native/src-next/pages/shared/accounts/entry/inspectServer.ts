import type {Dispatch, MutableRefObject, SetStateAction} from 'react';
import {Platform} from 'react-native';
import type {ResetFileLocation} from '../../../../../../../packages/client-core/src/account-reset';
import type {PublicInvitation} from '../../../../../../../packages/client-core/src/direct-account';
import {routeOrigin} from '../../../../../../../packages/client-core/src/route-identity';
import {firstAnswering, serverAddressCandidates} from '../../../../../../../packages/client-core/src/server-address';
import type {ServerSignIn} from '../../../../../../../packages/client-core/src/sign-in';
import {nativeAccounts} from '../../../../platform/accounts';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import type {TVCodeKind} from '../../../../screens/accounts/tv-code-opening';
import {SETUP_ELSEWHERE} from '../../../../pages/shared/accounts/entry/entry-helpers';
import type {
  InvitationEntryOpening,
  InvitationIntent,
  InvitationOpening,
  InvitationSwitch,
  NativeAccountEntryStage,
  OwnerResetIntent,
  OwnerResetOpening,
} from '../../../../pages/shared/accounts/entry/entry-types';

export type InspectServerParams = {
  origin: string;
  lifetime: MutableRefObject<boolean>;
  stageRef: MutableRefObject<NativeAccountEntryStage>;
  operation: MutableRefObject<NativeServerSignIn | undefined>;
  endedTarget: MutableRefObject<Readonly<{serverId: string; fingerprint: string}> | undefined>;
  identityOrigin: MutableRefObject<string | undefined>;
  ownerResetIntent: MutableRefObject<OwnerResetIntent | undefined>;
  ownerResetOpening: MutableRefObject<OwnerResetOpening | undefined>;
  invitationIntent: MutableRefObject<InvitationIntent | undefined>;
  invitationOpening: MutableRefObject<InvitationOpening | undefined>;
  invitationEntryOpening: MutableRefObject<InvitationEntryOpening | undefined>;
  invitationSwitch: MutableRefObject<InvitationSwitch | undefined>;
  retireOwnerRecoveryContact: () => void;
  retireOwnerReset: () => void;
  retireRecovery: () => boolean;
  startTV: (kind: TVCodeKind, source?: NativeServerSignIn) => Promise<void>;
  setStage: Dispatch<SetStateAction<NativeAccountEntryStage>>;
  setServer: Dispatch<SetStateAction<ServerSignIn | undefined>>;
  setIdentityAddress: Dispatch<SetStateAction<string>>;
  setIdentityCode: Dispatch<SetStateAction<string>>;
  setIdentityWrong: Dispatch<SetStateAction<boolean>>;
  setSetupAddress: Dispatch<SetStateAction<string>>;
  setName: Dispatch<SetStateAction<string>>;
  setUsername: Dispatch<SetStateAction<string>>;
  setPassword: Dispatch<SetStateAction<string>>;
  setSetupPasswordConfirm: Dispatch<SetStateAction<string>>;
  setCode: Dispatch<SetStateAction<string>>;
  setResetLocation: Dispatch<SetStateAction<ResetFileLocation | undefined>>;
  setInvitationPreview: Dispatch<SetStateAction<PublicInvitation | undefined>>;
};

/** Inspects a server address and transitions into identity-code, setup, invitation, owner-reset, uses-portico, or local sign-in. */
export async function inspectServerOrigin({
  origin, lifetime, stageRef, operation, endedTarget, identityOrigin,
  ownerResetIntent, ownerResetOpening, invitationIntent, invitationOpening, invitationEntryOpening, invitationSwitch,
  retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, startTV,
  setStage, setServer, setIdentityAddress, setIdentityCode, setIdentityWrong, setSetupAddress,
  setName, setUsername, setPassword, setSetupPasswordConfirm, setCode, setResetLocation, setInvitationPreview,
}: InspectServerParams): Promise<NativeServerSignIn | undefined> {
  const resetIntent = ownerResetIntent.current, invite = invitationIntent.current;
  retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose();
  const next = operation.current = new NativeServerSignIn(nativeAccounts, {
    requiredSetupHandoff: Platform.isTV,
    expectedTarget: endedTarget.current,
    expectedInvitation: invite ? {origin: invite.server, serverId: invite.id, fingerprint: invite.fingerprint, signIn: invite.signIn, expiresAt: invite.expiresAt} : undefined,
  });
  const found = await firstAnswering(serverAddressCandidates(origin), candidate => next.inspect(candidate));
  const value = found.value;
  if (!lifetime.current) return;
  const inspected = found.origin;
  if (next.needsIdentityCode()) {
    routeOrigin(inspected); identityOrigin.current = inspected;
    setIdentityAddress(inspected);
    setServer(undefined); setIdentityCode(''); setIdentityWrong(false); setStage('identity-code'); return;
  }
  if (!value) throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
  if (resetIntent && (ownerResetIntent.current !== resetIntent || routeOrigin(inspected) !== resetIntent.origin || value.id !== resetIntent.serverId || value.signIn !== 'portico' || value.setupRequired)) {
    throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
  }
  setServer(value);
  if (value.setupRequired && !Platform.isTV) {
    setSetupAddress(routeOrigin(inspected));
    setName(value.name); setUsername(''); setPassword(''); setSetupPasswordConfirm(''); setStage('setup-confirm'); return;
  }
  if (value.setupRequired) throw Object.assign(new Error('native_setup_phone_required'), {code: SETUP_ELSEWHERE});
  if (value.signIn === 'portico') {
    if (invite?.publicPreview) {
      try {
        const result = await next.previewPorticoInvitation(invite.raw);
        if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
        invitationIntent.current = result.entry; invitationSwitch.current = {entry: result.entry, active: true};
        setInvitationPreview(result.preview); stageRef.current = 'invitation-account'; setStage('invitation-account'); return;
      } catch (err) {
        if (lifetime.current && operation.current === next && invitationIntent.current === invite) {
          invitationEntryOpening.current = {active: true}; stageRef.current = 'invitation-entry'; setStage('invitation-entry');
        }
        throw err;
      }
    }
    if (invite) {
      const controller = await next.preparePorticoInvitation();
      if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
      const ticket = {source: next, controller, active: true, kind: 'portico' as const}; invitationOpening.current = ticket;
      stageRef.current = 'invitation-code'; setStage('invitation-code');
      const preview = await controller.preview(invite.raw);
      if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite || invitationOpening.current !== ticket || !ticket.active) return;
      invitationIntent.current = undefined; setInvitationPreview(preview); setCode(''); setPassword(''); setSetupPasswordConfirm('');
      stageRef.current = 'invitation-portico'; setStage('invitation-portico'); return;
    }
    if (resetIntent) {
      const ticket = {source: next, controller: new AbortController()}; ownerResetOpening.current = ticket;
      await next.prepareOwnerReset();
      if (lifetime.current && operation.current === next && ownerResetOpening.current === ticket && ownerResetIntent.current === resetIntent && !ticket.controller.signal.aborted) {
        ownerResetIntent.current = undefined; setCode(''); setResetLocation(undefined); setStage('owner-reset');
      }
    } else setStage('uses-portico');
    return;
  }
  await next.confirmLocal(await next.inspectLocalIdentity());
  if (invite) {
    if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
    const controller = next.createLocalInvitation(), ticket = {source: next, controller, active: true, kind: 'local' as const}; invitationOpening.current = ticket;
    stageRef.current = 'invitation-code'; setStage('invitation-code');
    const preview = await controller.preview(invite.raw);
    if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite || invitationOpening.current !== ticket || !ticket.active) return;
    invitationIntent.current = undefined; setInvitationPreview(preview); setCode(''); setPassword(''); setSetupPasswordConfirm(''); setUsername(''); setName('');
    stageRef.current = 'invitation-local'; setStage('invitation-local'); return;
  }
  if (lifetime.current) {setServer(next.serverDescriptor()); if (Platform.isTV) await startTV('local'); else setStage('local');}
  return next;
}
