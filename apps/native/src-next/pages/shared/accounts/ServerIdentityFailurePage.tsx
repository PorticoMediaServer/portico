import React from 'react';
import type {NativeIdentityFailure} from '../../../platform/identity-failure';
import {ServerIdentityFailureView} from '../../../kit/accounts/ServerIdentityFailureView';
import {useServerIdentityFailure} from './useServerIdentityFailure';

/** The server at this address isn't the one this device knew (phone, tablet and TV). It cannot construct an API or send
 * a token to the address that answered; it only retries, chooses another server, or forgets the old one. */
export function ServerIdentityFailurePage({failure, onRetry, onChoose, onForget, onError}: {
  failure: NativeIdentityFailure; onRetry: () => void; onChoose: () => void; onForget: () => Promise<void>; onError: (error: unknown) => void;
}) {
  const identity = useServerIdentityFailure({onChoose, onForget, onError});
  return <ServerIdentityFailureView failure={failure} busy={identity.busy} confirm={identity.confirm} onConfirmChange={identity.setConfirm}
    cancelRef={identity.cancelRef} openerRef={identity.openerRef} onRetry={onRetry} onChoose={onChoose} onForget={identity.forget}/>;
}
