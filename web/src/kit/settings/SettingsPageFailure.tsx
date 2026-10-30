import {defaultI18n} from '@i18n';
import {StateLayout} from '../states';
import styles from './settings.module.css';

/** Section 17 uses the drawn page failure while Settings keeps its own heading and navigation. */
export function SettingsPageFailure({retry, home}: {retry: () => void;home: () => void}) {
  return <StateLayout backdrop={null} className={styles.pageFailure} icon="i-warn" tone="warning"
    title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: defaultI18n.t('next.states.tryAgain'), icon: 'i-retry', onPress: retry}}
    secondary={{id: 'home', label: defaultI18n.t('next.states.goHome'), onPress: home}} />;
}
