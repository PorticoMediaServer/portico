import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Tabs} from '../../../kit/controls';
import {NotificationsFrame} from '../../../kit/frames/NotificationsFrame';
import {NotificationsList} from '../../../kit/shell/NotificationsList';
import {Menu} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {OwnFeedbackReport} from '../../../kit/shell/OwnFeedbackReport';
import {useNotifications, type NotificationsPageProps} from '../../shared/notifications/useNotifications';

export function NotificationsPage(props: NotificationsPageProps) {
  const {t} = props.runtime.text, model = useNotifications(props);
  const title = t('next.shell.notifications');
  if (model.retired) return <NotificationsFrame testID="native-notifications-retired" title={title} onClose={model.close}>
    <StateView icon="i-warn" title={t('notifications.error.load')} primaryAction={{id: 'back', label: t('action.back'), onPress: model.close}}/>
  </NotificationsFrame>;
  if (!model.admitted) return <NotificationsFrame testID="native-notifications-pending" title={title} onClose={model.close}/>;
  const {view, unread, busy, chosen, chosenWords, menu, report} = model;
  const back = {label: model.backLabel, testID: 'notifications-back', onPress: model.back};
  const markAll = view !== 'archived' && unread > 0 ? <Button testID="notifications-mark-all" variant="text" size="small" disabled={busy} onPress={model.markAll}>{t('notifications.markAllRead')}</Button> : undefined;
  return <NotificationsFrame testID="native-notifications" title={title} onClose={model.close} back={back} trailing={markAll}>
    <Tabs testID="notifications-views" value={view} onValueChange={model.changeView} items={[
      {value: 'unread', label: t('notifications.view.unread'), count: unread > 0 ? defaultI18n.number(unread) : undefined},
      {value: 'all', label: t('notifications.view.all')},
      {value: 'archived', label: t('notifications.view.archived')},
    ]}/>
    <NotificationsList model={model} t={t}/>
    <Menu testID="notification-actions" open={chosen !== undefined} onOpenChange={open => {if (!open) model.closeMenu();}} presentation="sheet"
      title={chosenWords?.title} description={chosenWords?.body} items={menu}/>
    {report && <OwnFeedbackReport api={props.context.api} reportId={report} onClose={model.closeReport}/>}
  </NotificationsFrame>;
}
