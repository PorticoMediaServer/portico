import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {TextInput} from 'react-native';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {NativeMemberAccountMove} from '../../../platform/member-account-move';

/** Connect41b: the Waiting account proves its old password to the move client, never to ordinary password sign-in.
 * The typed password is cleared as soon as it is handed over, on Back, and whenever the move leaves this step. */
export function useMovePassword<Result>(controller: NativeMemberAccountMove<Result>, onBack: () => void) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [username, setUsername] = useState(''), [password, setPassword] = useState(''), [shown, setShown] = useState(false);
  const passwordInput = useRef<TextInput>(null);
  useEffect(() => {if (state.phase !== 'password') {setPassword(''); setShown(false);}}, [state.phase]);
  const forget = () => {setPassword(''); setShown(false);};
  const ready = !state.busy && !!username && !!password;
  return {
    visible: state.phase === 'password', busy: state.busy, ready,
    error: state.error != null ? presentError(state.error, 'sign-in').body : undefined,
    username, setUsername, password, setPassword, shown, toggleShown: () => setShown(value => !value), passwordInput,
    next: () => {if (!state.busy) passwordInput.current?.focus();},
    submit: () => {
      if (!ready) return;
      const supplied = password; forget();
      void controller.proveOldPassword(username, supplied);
    },
    back: () => {if (!state.busy) {forget(); controller.cancel(); onBack();}},
  };
}
