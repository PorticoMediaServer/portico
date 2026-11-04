import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {InboxSnapshot, InboxView} from '@core/inbox.ts';
import type {NotificationAudience} from '@core/notifications.ts';
import {Button, Segmented} from '../controls';
import styles from './account.module.css';

const t = defaultI18n.t;
/** desktop-search Notifications. Counts/audiences are genuine inbox facts;
 * the renderer does not synthesize a Server badge or a notification subject. */
/** `footer` closes the panel (the bell's See all); `pages` false is the bell's panel: no Load more (See all opens the page). */
/** An empty or failed inbox, inset like the header and tabs above it (desktop-search Notifications). */
export function NotificationsPanelState({children}: {children: ReactNode}) {
  return <div className={styles.inboxState}>{children}</div>;
}
export function NotificationsPanel({snapshot, onAudience, onView, onReadAll, onMore, children, footer, pages = true}: {
  snapshot: InboxSnapshot; onAudience: (value: NotificationAudience) => void; onView: (value: InboxView) => void;
  onReadAll: () => void; onMore: () => void; children: ReactNode; footer?: ReactNode; pages?: boolean;
}) {
  // A page lets the feed flow with the document; the bell's panel scrolls inside the window, its footer kept in view.
  return <section aria-label={t('notifications.title')} data-notifications={pages ? 'page' : 'panel'}>
    <header className={styles.inboxHeader}><h3>{t('notifications.title')}</h3><Button variant="text" size="xs" icon="i-check"
      disabled={snapshot.phase !== 'ready' || snapshot.counts.unread === 0} onClick={onReadAll}>{t('notifications.markAllRead')}</Button></header>
    <div className={styles.inboxTabs}>
      <Segmented aria-label={t('web.notifications.audience')} value={snapshot.audience} items={snapshot.audiences.map(value => {
        // desktop-search: the audience not on screen carries its unread count ("Server 1").
        const label = t(value === 'account-admin' ? 'notifications.audience.admin' : 'notifications.audience.profile'), unread = value === snapshot.audience ? 0 : snapshot.unreadBy[value] ?? 0;
        return {value, label: unread > 0 ? <>{label}<span className={styles.audienceCount}>{defaultI18n.number(unread)}</span></> : label,
          accessibleLabel: unread > 0 ? t('web.notifications.audienceUnread', {audience: label, count: unread}) : undefined};
      })}
        onValueChange={value => {const known = snapshot.audiences.find(item => item === value); if (known) onAudience(known);}} />
      <Segmented aria-label={t('web.notifications.show')} value={snapshot.view} items={(['unread', 'all', 'archived'] as const).map(value => ({value, label: t(`notifications.view.${value}`)}))}
        onValueChange={value => {if (value === 'unread' || value === 'all' || value === 'archived') onView(value);}} />
    </div>
    <div className={styles.inboxBody}>{children}{pages && snapshot.more && <div className={styles.inboxMore}><Button size="small" loading={snapshot.busy} onClick={onMore}>{t('notifications.loadMore')}</Button></div>}</div>
    {footer && <footer className={styles.inboxFooter}>{footer}</footer>}
  </section>;
}
