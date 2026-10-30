import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {AuthStage} from '../../kit/accounts/AuthStage';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {ServicesFacts, ServicesNotice, ServicesQuiet, HostedError} from '../../kit/accounts/ServicesAuth';
import styles from './setup.module.css';

const t = defaultI18n.t;

export type HostedSetupApprovalProps = Readonly<{
  phase?: 'loading' | 'review' | 'approved' | 'finishing' | 'denied' | 'returning' | 'error' | 'expired';
  serverName: string;
  address: string;
  accountName: string;
  requestedAt: string;
  approved?: boolean;
  /** Hosted reports the claim installed; Return is offered only then. Defaults true for display-only callers. */
  installed?: boolean;
  /** Still finishing long after Approve: say so, with what to check. */
  slow?: boolean;
  busy?: boolean;
  error?: unknown;
  onApprove: () => void;
  onDeny: () => void;
  onReturn: () => void;
}>;

/** auth2b/2c display-only. The strict Hosted lookup and decision controller are
 * Security's; this draws the review, the connected page and the shared failure
 * shape without switch wording, extra people counts or source authority. */
export function HostedSetupApproval({phase, serverName, address, accountName, requestedAt, approved = false, installed = true, slow = false, busy = false, error, onApprove, onDeny, onReturn}: HostedSetupApprovalProps) {
  const steps = <div className={styles.steps} aria-label={t('web.setup.stepsLabel')}>
    <Step state="done" number={<Icon name="i-check" size={18}/>} title={t('web.setup.stepServer')} body={serverName}/>
    <Step state={approved ? 'done' : 'current'} number={approved ? <Icon name="i-check" size={18}/> : 2} title={t('web.setup.stepAccount')}
      body={approved ? t('web.claim.approved') : t('web.claim.approvalStep')}/>
    <Step state="next" number={3} title={t('web.setup.stepLibrary')} body={t('web.setup.stepLibraryBody')}/>
  </div>;
  if (phase === 'denied') return <div className={styles.host}>
    <AuthStage service contentClassName={styles.approvalContent}>
      <h1>{t('web.general.notConnectedBadge')}</h1>
      <p className={styles.lede}>{t('web.claim.denied', {server: serverName})}</p>
      {Boolean(error) && <HostedError error={error}/>}
      <div className={styles.btnRow}><Button variant="primary" size="hero" autoFocus loading={busy} disabled={busy} onClick={onReturn}>{t('web.claim.returnTo', {server: serverName})}</Button></div>
    </AuthStage>
  </div>;
  // auth2d: an expired or already-answered request is its own outcome, never the review page.
  if (phase === 'expired') return <div className={styles.host}>
    <AuthStage service contentClassName={styles.approvalContent}>
      <h1>{t('web.general.notConnectedBadge')}</h1>
      <p className={styles.lede}>{t('web.claim.expired')}</p>
      {Boolean(error) && <HostedError error={error}/>}
      <div className={styles.btnRow}><Button variant="primary" size="hero" autoFocus onClick={onReturn}>{t('web.claim.returnTo', {server: serverName})}</Button></div>
    </AuthStage>
  </div>;
  if (error && !approved) return <div className={styles.host}>
    <AuthStage service contentClassName={styles.approvalContent}>
      <h1>{t('web.claim.titleGeneric')}</h1>
      <HostedError error={error}/>
      <div className={styles.btnRow}><Button variant="primary" size="hero" onClick={onReturn}>{t('web.claim.returnTo', {server: serverName})}</Button></div>
    </AuthStage>
  </div>;
  if (approved) return <div className={styles.host}>
    <AuthStage service contentClassName={styles.approvalContent}>
      <p className={styles.okline}><Icon name="i-okc" size={20}/>{t('web.claim.connectedTitle')}</p>
      <h1>{t('web.setup.youOwn', {server: serverName})}</h1>
      <p className={styles.lede}>{t('web.claim.connected', {server: serverName})} {t('web.claim.nextFirstLibrary')}</p>
      {/* The approval stands even when the completion step failed: the same Return retries it. */}
      {Boolean(error) && <HostedError error={error}/>}
      {/* Not installed yet, or Return found it not ready: say so, with the same Return to try again. */}
      {installed && phase !== 'finishing'
        ? <div className={styles.btnRow}><Button variant="primary" size="hero" autoFocus loading={busy} disabled={busy} onClick={onReturn}>{t('web.claim.returnTo', {server: serverName})}</Button></div>
        : <><p role="status">{t('web.claim.finishingOnServer')}</p>
          {slow && <ServicesNotice tone="info">{t('web.claim.finishingSlow')}</ServicesNotice>}
          <div className={styles.btnRow}><Button variant="glass" size="hero" loading={busy} disabled={busy} onClick={onReturn}>{t('web.claim.tryAgain')}</Button></div></>}
      <ServicesQuiet>{t('web.claim.signedInAs', {account: accountName})}</ServicesQuiet>
    </AuthStage>
    {steps}
  </div>;
  return <div className={styles.host}>
    <AuthStage service contentClassName={styles.approvalContent}>
      <h1>{t('web.claim.title', {server: serverName})}</h1>
      <p className={styles.lede}>{t('web.claim.lede')}</p>
      <ServicesFacts items={[
        {id: 'server', label: t('web.claim.server'), value: serverName},
        {id: 'address', label: t('web.claim.address'), value: <span style={{fontVariantNumeric: 'tabular-nums'}}>{address}</span>},
        {id: 'account', label: t('web.claim.account'), value: accountName},
        {id: 'requested', label: t('web.claim.requested'), value: requestedAt},
      ]}/>
      <ServicesNotice tone="info"><b>{t('web.claim.becomesOwnerOf', {account: accountName, server: serverName})}</b> {t('web.claim.mainProfile', {account: accountName})}</ServicesNotice>
      <ServicesQuiet>{t('web.claim.checkAddress')}</ServicesQuiet>
      <div className={styles.btnRow}>
        <Button variant="primary" size="hero" autoFocus loading={busy} disabled={busy} onClick={onApprove}>{t('web.claim.approve')}</Button>
        <Button variant="glass" size="hero" disabled={busy} onClick={onDeny}>{t('web.claim.deny')}</Button>
      </div>
      <ServicesQuiet>{t('services.claim.returnHint', {server: serverName})}</ServicesQuiet>
    </AuthStage>
    {steps}
  </div>;
}

function Step({state, number, title, body}: {state: 'done' | 'current' | 'next'; number: ReactNode; title: string; body: string}) {
  return <div className={styles.step} data-state={state}><span className={styles.stepNumber}>{number}</span><span>{title}<small>{body}</small></span></div>;
}
