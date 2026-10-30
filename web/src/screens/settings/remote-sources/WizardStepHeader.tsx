import {defaultI18n} from '@i18n';
import styles from './wizard-step-header.module.css';

const t = defaultI18n.t;

/** The authored wizard step header: the five segments, then the existing
 * "Step N of 5" label and the caller's exact step title. It carries no step,
 * footer or form behaviour. */
export function WizardStepHeader({step, count, title}: Readonly<{step: number; count: number; title: string}>) {
  return <div className={styles.header}>
    <div className={styles.steps} aria-hidden="true">{Array.from({length: count}, (_, index) => index + 1).map(value =>
      <i key={value} data-on={value <= step || undefined}/>)}</div>
    <div className={styles.label}><span>{t('next.remoteSources.stepOf', {step})}</span><b>{title}</b></div>
  </div>;
}
