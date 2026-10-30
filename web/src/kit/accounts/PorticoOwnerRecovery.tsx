import type {ReactNode} from 'react';
import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {Icon} from '../Icon';
import {Button} from '../controls';
import {AuthStage} from './AuthStage';
import styles from './portico-owner-recovery.module.css';

const t = createI18n(resolveRegion(undefined, deviceRegion())).t;


/** One page shell for both views: the drawn 800 px column instead of the auth
 * stage's 440, and the full width a phone has. */
export function PorticoOwnerRecoveryStage({children}: {children: ReactNode}) {
  return <AuthStage shape="round" phoneShape="top" legal={false} className={styles.stage} contentClassName={styles.content}>{children}</AuthStage>;
}

/** auth 20d: the owner's one way in while Portico Services are away. The caller
 * owns writing the file, its location and every code; this only draws the ask. */
export function PorticoOwnerRecoveryRequest({serverName, busy = false, error, onWriteFile, onBack, onAccountRecovery}: {
  serverName: string;
  busy?: boolean;
  error?: ReactNode;
  onWriteFile: () => void;
  onBack: () => void;
  onAccountRecovery: () => void;
}) {
  return <PorticoOwnerRecoveryStage><section className={styles.page}>
    <p className={styles.why}>{serverName}</p>
    <h1>{t('accounts.signIn.cannotUsePortico')}</h1>
    <p className={styles.lede}>{t('accounts.recovery.porticoBody')}</p>
    {error}
    <div className={styles.actions}><Button variant="primary" loading={busy} disabled={busy} onClick={onWriteFile}>{t('accounts.recovery.writeFile')}</Button></div>
    <p className={styles.quiet}>{t('accounts.recovery.writeHelp')}</p>
    <button type="button" className={styles.optionButton} disabled={busy} onClick={onAccountRecovery}>
      <b>{t('accounts.recovery.notOwner')}</b>
      <span>{t('accounts.recovery.notOwnerBody')}</span>
    </button>
    <p className={styles.links}><button type="button" disabled={busy} onClick={onBack}><Icon name="i-left" size={18} />{t('accounts.recovery.back')}</button></p>
  </section></PorticoOwnerRecoveryStage>;
}

/** auth 20e: the code worked, so the owner is in for this session only. Both
 * drawn choices are explicit: nothing here changes the owner or the sign-in kind
 * on its own. */
export function PorticoOwnerRecoveryDone({serverName, busy = false, error, onChangeOwner, onSwitchToLocal, onContinue}: {
  serverName: string;
  busy?: boolean;
  error?: ReactNode;
  onChangeOwner: () => void;
  onSwitchToLocal: () => void;
  onContinue: () => void;
}) {
  return <PorticoOwnerRecoveryStage><section className={`${styles.page} ${styles.done}`}>
    {error}
    <p className={styles.worked}><Icon name="i-okc" size={20} />{t('accounts.recovery.ownerSessionWorked')}</p>
    <h1 className={styles.singleLine}>{t('accounts.recovery.ownerSessionTitle')}</h1>
    <p className={styles.lede}>{t('accounts.recovery.ownerSessionWhatNow')}</p>
    <div className={styles.option}>
      <p>{t('accounts.recovery.changeOwnerBody')}</p>
      <Button size="compact" disabled={busy} onClick={onChangeOwner}>{t('accounts.recovery.changeOwner')}</Button>
    </div>
    <div className={styles.option}>
      <p>{t('accounts.recovery.switchToLocalBody')}</p>
      <Button size="compact" disabled={busy} onClick={onSwitchToLocal}>{t('accounts.recovery.switchToLocal')}</Button>
    </div>
    <div className={styles.continueRow}><Button variant="primary" className={styles.continueButton} loading={busy} disabled={busy} onClick={onContinue}>{t('accounts.recovery.continueServer', {server: serverName})}</Button></div>
    <p className={styles.quiet}>{t('accounts.recovery.ownerSessionLater')}</p>
  </section></PorticoOwnerRecoveryStage>;
}
