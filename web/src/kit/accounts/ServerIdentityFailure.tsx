import {useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
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

/** Server-identity 6a/6b. No explicit pin confirmation or Trust fallback. */
export function ServerIdentityFailure({serverName, failure, busy, onRetry, onChooseServer, onForget}: ServerIdentityFailureProps) {
  const [confirmation, setConfirmation] = useState<Readonly<{name: string; forget: () => Promise<void>}>>(), [forgetting, setForgetting] = useState(false), [error, setError] = useState<unknown>();
  const cancel = useRef<HTMLButtonElement>(null), trigger = useRef<HTMLButtonElement>(null);
  const changed = failure === 'local-changed';
  const forget = async () => {
    if (forgetting || !confirmation) return;
    const captured = confirmation;
    setForgetting(true); setError(undefined);
    try {await captured.forget(); setConfirmation(undefined);} catch (failure) {setError(failure);} finally {setForgetting(false);}
  };
  return <>
    <section className={styles.identityFailurePanel}>
      <span className={styles.identityFailureIcon} data-changed={changed || undefined}><Icon name={changed ? 'i-shieldx' : 'i-question'} size={28} /></span>
      <h3>{t('portico.failure.identity.title', {server: serverName})}</h3>
      <p>{t(changed ? 'connection.identity.changedBody' : 'portico.failure.identity.body')}</p>
      <div className={styles.identityFailureActions}>
        <Button variant="primary" icon="i-retry" iconSize={20} disabled={busy} onClick={onRetry}>{t('action.tryAgain')}</Button>
        <Button variant="glass" disabled={busy} onClick={onChooseServer}>{t('accounts.signIn.anotherServer')}</Button>
        {changed && <button ref={trigger} type="button" className={styles.identityForgetLink} disabled={busy} onClick={() => {setError(undefined); setConfirmation({name: serverName, forget: onForget});}}>{t('connection.identity.forget', {server: serverName})}</button>}
      </div>
    </section>
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
