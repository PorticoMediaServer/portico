import {useCallback, useLayoutEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction} from 'react';
import {Platform} from 'react-native';
import type {PublicInvitation} from '../../../../../../../packages/client-core/src/direct-account';
import type {NativeInvitationLink} from '../../../../platform/invitation-links';
import type {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import type {
  InvitationEntryOpening,
  InvitationIntent,
  InvitationOpening,
  InvitationSwitch,
  NativeAccountEntryStage,
} from '../../../../pages/shared/accounts/entry/entry-types';

type InvitationFlowParams = {
  incomingInvitation: NativeInvitationLink | undefined;
  lifetime: MutableRefObject<boolean>;
  busyRef: MutableRefObject<boolean>;
  acting: MutableRefObject<boolean>;
  stageRef: MutableRefObject<NativeAccountEntryStage>;
  operation: MutableRefObject<NativeServerSignIn | undefined>;
  account: MutableRefObject<NativePorticoSignIn | undefined>;
  preparedAuthSource: MutableRefObject<NativePorticoSignIn | undefined>;
  stopMemberMove: () => void;
  cancelDirectory: () => void;
  setBusy: Dispatch<SetStateAction<boolean>>;
  setStage: Dispatch<SetStateAction<NativeAccountEntryStage>>;
  setAddress: Dispatch<SetStateAction<string>>;
  setUsername: Dispatch<SetStateAction<string>>;
  setCode: Dispatch<SetStateAction<string>>;
  setPassword: Dispatch<SetStateAction<string>>;
  setSetupPasswordConfirm: Dispatch<SetStateAction<string>>;
  setError: Dispatch<SetStateAction<unknown>>;
};

/** Owns invitation entry/preview/switch openings, QR scan state, and incoming OS invitation links. */
export function useInvitationFlow(params: InvitationFlowParams) {
  const {incomingInvitation} = params;
  const paramsRef = useRef(params);
  paramsRef.current = params;

  const invitationIntent = useRef<InvitationIntent | undefined>(undefined);
  const invitationEpoch = useRef(0);
  const [invitationPreview, setInvitationPreview] = useState<PublicInvitation>();
  const invitationOpening = useRef<InvitationOpening | undefined>(undefined);
  const invitationEntryOpening = useRef<InvitationEntryOpening | undefined>(undefined);
  const invitationScan = useRef<AbortController | undefined>(undefined);
  const [invitationScanning, setInvitationScanning] = useState(false);
  const [scannedInvitationLink, setScannedInvitationLink] = useState<string>();
  const invitationSwitch = useRef<InvitationSwitch | undefined>(undefined);

  const retireInvitationEntry = useCallback(() => {
    invitationScan.current?.abort(); invitationScan.current = undefined; setInvitationScanning(false);
    if (invitationEntryOpening.current) invitationEntryOpening.current.active = false;
    invitationEntryOpening.current = undefined;
  }, []);
  const retireInvitationSwitch = useCallback(() => {
    if (invitationSwitch.current) invitationSwitch.current.active = false;
    invitationSwitch.current = undefined;
  }, []);
  const retireInvitation = useCallback(() => {
    const ticket = invitationOpening.current;
    if (ticket) {ticket.active = false; ticket.controller.dispose(); invitationOpening.current = undefined;}
    setInvitationPreview(undefined);
  }, []);
  const disposeInvitationOnUnmount = useCallback(() => {
    retireInvitationEntry();
    if (invitationIntent.current) paramsRef.current.operation.current?.dispose();
    const switching = invitationSwitch.current; retireInvitationSwitch();
    if (switching) paramsRef.current.account.current?.dispose();
    const ticket = invitationOpening.current;
    if (ticket) {ticket.active = false; invitationOpening.current = undefined; ticket.source.dispose();}
  }, [retireInvitationEntry, retireInvitationSwitch]);

  const openInvitationEntry = useCallback((returnAddress?: string, external?: NativeInvitationLink) => {
    const p = paramsRef.current;
    if (!p.lifetime.current || p.busyRef.current || p.acting.current || external && !external.current()) return;
    const externalTarget = external ? NativeServerSignIn.parseInvitationEntry(external.raw, '') : undefined;
    if (external && !externalTarget) return;
    retireInvitationEntry(); retireInvitationSwitch(); retireInvitation(); p.stopMemberMove(); p.cancelDirectory(); p.operation.current = undefined;
    p.account.current?.dispose(); p.account.current = undefined; p.preparedAuthSource.current = undefined;
    invitationIntent.current = undefined; invitationEntryOpening.current = {active: true, returnAddress, external};
    p.setAddress(externalTarget?.server ?? returnAddress ?? ''); p.setUsername(''); p.setCode(''); p.setPassword(''); p.setSetupPasswordConfirm(''); p.setError(undefined);
    setScannedInvitationLink(undefined); setInvitationScanning(false);
    p.stageRef.current = 'invitation-entry'; p.setStage('invitation-entry');
  }, [retireInvitation, retireInvitationEntry, retireInvitationSwitch]);

  useLayoutEffect(() => {
    if (!incomingInvitation || Platform.isTV || !incomingInvitation.current()) return;
    const p = paramsRef.current;
    p.lifetime.current = true; p.acting.current = false; p.busyRef.current = false; p.setBusy(false);
    openInvitationEntry(undefined, incomingInvitation);
    let active = true;
    const retire = () => {
      if (!active) return; active = false; invitationEpoch.current++;
      const cur = paramsRef.current;
      cur.lifetime.current = false; retireInvitationEntry(); retireInvitation();
      cur.operation.current?.dispose(); cur.account.current?.dispose();
    };
    const off = incomingInvitation.onRetired(retire);
    if (!incomingInvitation.current()) retire();
    return () => {off(); retire();};
  }, [incomingInvitation, openInvitationEntry, retireInvitation, retireInvitationEntry]);

  return {
    invitationIntent, invitationEpoch,
    invitationPreview, setInvitationPreview,
    invitationOpening, invitationEntryOpening,
    invitationScan, invitationScanning, setInvitationScanning,
    scannedInvitationLink, setScannedInvitationLink,
    invitationSwitch,
    retireInvitationEntry, retireInvitationSwitch, retireInvitation, disposeInvitationOnUnmount,
    openInvitationEntry,
  };
}
