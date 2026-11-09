import {defaultI18n} from '@i18n';
import styles from './pending-changes.module.css';

/** How many of this profile's edits are kept on this device, waiting for the server. */
export function PendingChanges({count}: {count: number}) {
  return <span className={styles.trigger} role="status">{defaultI18n.t('sync.waiting', {count})}</span>;
}
