import {Icon} from '../../kit/Icon';
import {t} from './shared';
import styles from './dashboard-quiet.module.css';

/** Presentation only; callers establish that the original read is ready and empty. */
export function DashboardQuiet({kind, phone = false}: {kind: 'alerts' | 'tasks'; phone?: boolean}) {
  if (kind === 'tasks') return <span className={styles.task}>{t('next.dashboard.nothingRunning')}</span>;
  return <div className={styles.calm} data-phone={phone || undefined}>
    <span className={styles.lead}><Icon name="i-check" size={18}/></span>
    <b>{t('web.overview.noAlerts')}</b>
  </div>;
}
