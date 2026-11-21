import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Tabs} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame, SheetFrame, StateRow} from '../../../kit/frames';
import {Menu} from '../../../kit/overlays';
import {NotificationItem, NotificationSubject} from '../../../kit/shell';
import {OwnFeedbackReport} from '../../../kit/shell/OwnFeedbackReport';
import {useTheme} from '../../../kit/theme';
import {useNotifications, type NotificationsPageProps} from '../../shared/notifications/useNotifications';

/** Notifications (phone-app 21, R9 `notifications`): on a phone a page in the Settings sheet, "‹ Settings · Notifications ·
 * Mark all read"; on a tablet the same page on its own. Unread / All / Archived, then each audience's notices as one card. */
export function NotificationsPage(props: NotificationsPageProps) {
  const {t} = props.runtime.text, theme = useTheme(), view = useNotifications(props);
  const title = t('next.shell.notifications');
  const markAll = view.view !== 'archived' && view.unread > 0
    ? {label: t('notifications.markAllRead'), onPress: view.markAll, disabled: view.busy, testID: 'notifications-mark-all'}
    : undefined;
  const failed = !view.loading && !view.items.length && view.error !== undefined;
  const page = view.retired
    ? <SettingsPageFrame title={title} state={{icon: 'i-warn', title: t('notifications.error.load'),
        primaryAction: {id: 'back', label: t('action.back'), onPress: view.close}}}/>
    : <SettingsPageFrame testID="native-notifications" title={title} action={markAll}
      back={{label: view.backLabel, testID: 'notifications-back', onPress: view.back}}
      tabs={<Tabs testID="notifications-views" value={view.view} onValueChange={view.changeView} items={[
        {value: 'unread', label: t('notifications.view.unread'), count: view.unread > 0 ? defaultI18n.number(view.unread) : undefined},
        {value: 'all', label: t('notifications.view.all')},
        {value: 'archived', label: t('notifications.view.archived')},
      ]}/>}>
      {(!view.admitted || view.loading && !view.items.length) && <SectionCard><StateRow kind="loading"/></SectionCard>}
      {failed && <SectionCard><StateRow kind="error" icon="i-warn" title={t('notifications.error.load')} reason={view.error}
        action={{label: t('action.tryAgain'), onPress: view.reload}}/></SectionCard>}
      {view.ready && !view.items.length && view.error === undefined && <SectionCard>
        <StateRow kind="empty" icon={view.view === 'archived' ? 'i-archive' : 'i-bell'} title={view.emptyTitle} reason={t('notifications.emptyBody')}/>
      </SectionCard>}
      {view.error !== undefined && view.items.length > 0 && <SectionCard>
        <StateRow kind="error" icon="i-warn" title={view.error} action={{label: t('action.tryAgain'), onPress: view.reload}}/>
      </SectionCard>}
      {view.sections.map(section => <SectionCard key={section.id} label={section.title}>
        {section.state.items.map((notice, index) => {
          const words = view.words(notice);
          return <NotificationItem key={notice.id} testID={`notification-${notice.id}`} separator={index > 0} notification={notice}
            title={words.title} body={words.body} when={defaultI18n.relativeTime(notice.createdAt)}
            subject={<NotificationSubject source={notice.source} severity={notice.severity}/>}
            actions={view.actionsFor(notice, view.view)} menuLabel={t('next.shell.native.moreOptions')} onMenu={() => view.showMenu(notice)}/>;
        })}
        {section.state.more && <Button align="center" testID={`notifications-more-${section.id}`} variant="glass" size="small"
          loading={section.state.busy} onPress={() => view.loadMore({kind: 'more', key: `more:${section.id}`, section})}>
          {t('notifications.loadMore')}
        </Button>}
      </SectionCard>)}
    </SettingsPageFrame>;
  return <>
    {theme.platform === 'phone'
      ? <SheetFrame kind="full" testID="native-notifications-sheet" open onOpenChange={open => {if (!open) view.close();}}>
          {page}
        </SheetFrame>
      : page}
    <Menu testID="notification-actions" open={view.chosen !== undefined} onOpenChange={open => {if (!open) view.closeMenu();}}
      presentation="sheet" title={view.chosenWords?.title} description={view.chosenWords?.body} items={view.menu}/>
    {view.report && <OwnFeedbackReport api={props.context.api} reportId={view.report} onClose={view.closeReport}/>}
  </>;
}
