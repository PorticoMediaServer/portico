import {defaultI18n} from '@i18n';
import styles from './advanced-analysis-consent.module.css';

const t = defaultI18n.t;

/** The single Root64 advanced-analysis consent fact the draft actually carries.
 * It draws no second gate or default and keeps the caller's exact callback. */
export function AdvancedAnalysisConsent({checked, disabled, onChange}: Readonly<{
  checked: boolean;
  disabled: boolean;
  onChange: (value: boolean) => void;
}>) {
  return <label className={styles.consent}>
    <input type="checkbox" className={styles.check} checked={checked} disabled={disabled}
      aria-label={t('next.remoteSources.allowAdvancedMediaAnalysis')}
      onChange={event => onChange(event.target.checked)}/>
    <span className={styles.text}>
      <b>{t('next.remoteSources.allowAdvancedMediaAnalysis')}</b>
      <span className={styles.warning}>{t('next.remoteSources.allowAdvancedMediaAnalysisWarning')}</span>
    </span>
  </label>;
}
