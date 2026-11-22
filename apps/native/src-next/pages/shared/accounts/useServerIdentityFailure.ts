import {useRef, useState} from 'react';
import type {View} from 'react-native';

/** Forget on a changed server identity: one confirmation, one flight; a failure goes to the caller, success chooses
 * another server. Moved from the old screen as it was. */
export function useServerIdentityFailure({onChoose, onForget, onError}: {onChoose: () => void; onForget: () => Promise<void>; onError: (error: unknown) => void}) {
  const [confirm, setConfirm] = useState(false), [busy, setBusy] = useState(false);
  const cancelRef = useRef<View>(null), openerRef = useRef<View>(null);
  const forget = async () => {
    if (busy) return; setBusy(true);
    try {await onForget(); onChoose();} catch (error) {onError(error);} finally {setBusy(false);}
  };
  return {confirm, setConfirm, busy, cancelRef, openerRef, forget: () => {void forget();}};
}
