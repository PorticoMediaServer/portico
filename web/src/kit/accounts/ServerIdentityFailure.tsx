import {useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../states';
import {Button} from '../controls/Button';
import {Dialog} from '../overlays/Dialog';
import {AuthError} from './AuthStage';
import styles from './accounts.module.css';

const t = defaultI18n.t;
export type ServerIdentityFailureProps = Readonly<{
  /** The expected saved server's last verified name, never the new address's name. */
  serverName: string;
  failure: 'local-changed' | 'unconfirmed';
  busy?: boolean;
  onRetry: () => void; onChooseServer: () => void;
  /** The caller fences its mounted private resources before removing only the
   * captured saved family locally. It must never contact the failed address. */
  onForget: () => Promise<void>;
}>;

/** Server-identity 6a/6b, on states.html's full-page state layout (connect 2), as the mock-up's
 * panels stand for: desktop holds the left column, a tablet or phone centres it and stacks the
 * buttons (frame 1a). `after` is the caller's own error line. No explicit pin confirmation or
 * Trust fallback. */
export function ServerIdentityFailure({serverName, failure, busy, onRetry, onChooseServer, onForget, after}: ServerIdentityFailureProps & {after?: ReactNode}) {
  const [confirmation, setConfirmation] = useState<Readonly<{name: string; forget: () => Promise<void>}>>(), [forgetting, setForgetting] = useState(false), [error, setError] = useState<unknown>();
  const cancel = useRef<HTMLButtonElement>(null), trigger = useRef<HTMLButtonElement | null>(null);
  const changed = failure === 'local-changed';
  const forget = async () => {
    if (forgetting || !confirmation) return;
    const captured = confirmation;
    setForgetting(true); setError(undefined);
    try {await captured.forget(); setConfirmation(undefined);} catch (failure) {setError(failure);} finally {setForgetting(false);}
  };
  // Safari doesn't focus a pressed button, so the dialog returns focus to the button pressed.
  const openForget = (event?: {currentTarget?: unknown}) => {
    trigger.current = event?.currentTarget instanceof HTMLButtonElement ? event.currentTarget : null;
    setError(undefined); setConfirmation({name: serverName, forget: onForget});
  };
  return <>
    <StateLayout backdrop={null} tone={changed ? 'error' : 'warning'} icon={changed ? 'i-shieldx' : 'i-question'}
      title={t('portico.failure.identity.title', {server: serverName})} body={t(changed ? 'connection.identity.changedBody' : 'portico.failure.identity.body')}
      primary={{id: 'retry', label: t('action.tryAgain'), icon: 'i-retry', disabled: busy, onPress: onRetry}}
      secondary={{id: 'choose', label: t('accounts.signIn.anotherServer'), disabled: busy, onPress: onChooseServer}}
      links={changed ? [{id: 'forget', label: t('connection.identity.forget', {server: serverName}), disabled: busy, onPress: openForget}] : undefined}
      afterActions={after} />
    <Dialog open={changed && Boolean(confirmation)} onOpenChange={open => {if (!open && !forgetting) {setConfirmation(undefined); setError(undefined);}}}
      title={t('connection.identity.forgetTitle', {server: confirmation?.name ?? serverName})} description={t('connection.identity.forgetBody')}
      initialFocusRef={cancel} returnFocusRef={trigger} showCloseButton={false} dismissOnOutsidePress={!forgetting} dismissOnBack={!forgetting}
      className={styles.identityForgetDialog} role="alertdialog"
      footer={<><Button ref={cancel} variant="glass" disabled={forgetting} onClick={() => setConfirmation(undefined)}>{t('action.cancel')}</Button>
        <Button variant="glass" className={styles.identityForgetDanger} loading={forgetting} onClick={() => void forget()}>{t('connection.identity.forgetAction')}</Button></>}>
      <AuthError error={error} />
    </Dialog>
  </>;
}
