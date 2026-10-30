import {useEffect, useMemo, useState} from 'react';
import {defaultI18n} from '@i18n';
import {scheduleSearchResultStatus} from './result-status';
import styles from './search.module.css';

export function SearchResultStatus({count, identity}: {count?: number; identity: string}) {
  const message = count !== undefined && count > 0 ? defaultI18n.t('web.search.results', {count}) : '';
  const request = useMemo(() => ({identity, message}), [identity, message]);
  const [announced, setAnnounced] = useState<typeof request>();
  useEffect(() => {
    if (!request.message) return;
    return scheduleSearchResultStatus(request.message, () => setAnnounced(request));
  }, [request]);
  return <span role="status" aria-atomic="true" className={styles.resultStatus}>{announced === request ? message : ''}</span>;
}
