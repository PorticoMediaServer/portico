import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {InboxSnapshot, InboxView} from '@core/inbox.ts';
import type {NotificationAudience} from '@core/notifications.ts';
import {Button, Segmented} from '../controls';
import styles from './account.module.css';

const t = defaultI18n.t;
/** desktop-search Notifications. Counts/audiences are genuine inbox facts;
 * the renderer does not synthesize a Server badge or a notification subject. */
export function NotificationsPanel({snapshot, onAudience, onView, onReadAll, onMore, children}: {
  snapshot: InboxSnapshot; onAudience: (value: NotificationAudience) => void; onView: (value: InboxView) => void;
  onReadAll: () => void; onMore: () => void; children: ReactNode;
}) {
  return <section aria-label={t('notifications.title')}>
    <header className={styles.inboxHeader}><h3>{t('notifications.title')}</h3><Button variant="text" size="xs" icon="i-check"
      disabled={snapshot.phase !== 'ready' || snapshot.counts.unread === 0} onClick={onReadAll}>{t('notifications.markAllRead')}</Button></header>
    <div className={styles.inboxTabs}>
      <Segmented aria-label={t('web.notifications.audience')} value={snapshot.audience} items={snapshot.audiences.map(value => ({value, label: t(value === 'account-admin' ? 'notifications.audience.admin' : 'notifications.audience.profile')}))}
        onValueChange={value => {const known = snapshot.audiences.find(item => item === value); if (known) onAudience(known);}} />
      <Segmented aria-label={t('web.notifications.show')} value={snapshot.view} items={(['unread', 'all', 'archived'] as const).map(value => ({value, label: t(`notifications.view.${value}`)}))}
        onValueChange={value => {if (value === 'unread' || value === 'all' || value === 'archived') onView(value);}} />
    </div>
    <div className={styles.inboxBody}>{children}{snapshot.more && <div className={styles.inboxMore}><Button size="small" loading={snapshot.busy} onClick={onMore}>{t('notifications.loadMore')}</Button></div>}</div>
  </section>;
}
