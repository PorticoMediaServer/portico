import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {noticeWords, type Notification, type NotificationAction} from '@core/notifications.ts';
import type {InboxAction} from '@core/inbox.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {NotificationItem} from '../../kit/shell/AccountSurfaces';
import {NotificationsPanel} from '../../kit/shell/NotificationsPanel';
import {Popover} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {Button} from '../../kit/controls';
import type {ShellPopoverProps} from '../../route-registry';
import styles from './notifications.module.css';
import {notificationDestination} from './notification-destination';
import {OwnFeedbackReport} from './OwnFeedbackReport';
import {startNotificationScan} from '@core/notification-actions.ts';
import {NotificationScreenAdmission} from './notification-screen-admission';

const t = defaultI18n.t;
/** How many notices the bell's panel shows; See all opens the whole feed as a page. */
export const BELL_NOTICES = 5;
type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;

/** The Notifications page: the whole feed, paged. Desktop draws it as a page of its own, never over
 * a remounted Home; the bell opens {@link NotificationsPopover} over whatever page is open. */
function NotificationsScreen() {
  const captured = useProtectedWebScreenContext(), phone = useShellWidth() < 900;
  if (!captured) return null;
  return phone ? <NotificationsFeed captured={captured} mode="page" /> : <div className={styles.page}><NotificationsFeed captured={captured} mode="page" /></div>;
}

/** desktop-search "Notifications": the bell opens a panel over the page, as today. It is a shell
 * popover, not a route, so opening and closing it never remounts the page under it. */
function NotificationsPopover({anchorRef, onClose}: ShellPopoverProps) {
  const captured = useProtectedWebScreenContext();
  if (!captured) return null;
  return <Popover open title={t('notifications.title')} accessibilityLabel={t('notifications.title')} anchorRef={anchorRef} returnFocusRef={anchorRef}
    onOpenChange={value => {if (!value) onClose();}} modal width={452} sideOffset={10}>
    <NotificationsFeed captured={captured} mode="popover" limit={BELL_NOTICES} onDone={onClose} />
  </Popover>;
}

function NotificationsFeed({captured, mode, limit, onDone}: {captured: Captured; mode: 'page' | 'popover'; limit?: number; onDone?: () => void}) {
  useShellFacts(captured.context);
  const {context} = captured, service = context.inbox, state = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const location = useRouterState({select: value => value.location});
  const [published, publish] = useState<Readonly<{context: typeof context; service: typeof service; location: typeof location; admission: NotificationScreenAdmission}>>();
  const owner = useRef<NotificationScreenAdmission | undefined>(undefined);
  const done = useRef(onDone);
  useLayoutEffect(() => {done.current = onDone;});
  useLayoutEffect(() => {
    // A page belongs to its route entry; the bell's panel belongs to its own opening.
    const admission = new NotificationScreenAdmission(service, () => context.isCurrent() && (mode === 'popover' || router.state.location === location));
    owner.current = admission;
    publish({context, service, location, admission});
    // A navigation retires the old callbacks before React cleanup, and closes the bell's panel.
    const off = router.subscribe('onBeforeNavigate', () => {admission.retire(); if (mode === 'popover') done.current?.();});
    return () => {
      admission.retire();off();
      if (owner.current === admission) owner.current = undefined;
      publish(value => value?.admission === admission ? undefined : value);
    };
  }, [context, service, location, mode]);
  const opening = published && published.context === context && published.service === service && published.location === location &&
    owner.current === published.admission && published.admission.current() ? published.admission : undefined;
  const phone = useShellWidth() < 900;
  // The root owns this shared inbox/badge service. Closing a panel unsubscribes
  // this view; authority retirement disposes the one real service at the root.
  useEffect(() => {opening?.initialize(state);}, [opening, state]);
  const close = () => {
    if (mode === 'popover') {done.current?.(); return;}
    opening?.leave(state, current => {if (current()) void router.navigate({to: '/'});});
  };
  const apply = (action: InboxAction, note: Notification) => {if (state.items.includes(note)) opening?.run(state, () => {void service.apply(action, [note.id]);});};
  const [report, setReport] = useState<string>();
  const go = (to: () => Promise<unknown>) => {void to(); if (mode === 'popover') done.current?.();};
  const open = (note: Notification, action: NotificationAction) => {
    if (!opening?.owns(state) || !state.items.includes(note) || !note.actions.includes(action)) return;
    const target = notificationDestination(action, context.facts.identity.libraries, context.facts.identity.capabilities?.manageServer === true, note);
    if (!target) return;
    // Commands done in place: the notice stays where it is (Dismiss archives it; Scan again starts the scan and marks it read).
    if (target.view === 'archive') {apply('archive', note); return;}
    if (target.view === 'own-report') {opening.run(state, () => {setReport(target.reportId); void service.apply('read', [note.id]);}); return;}
    if (target.view === 'scan') {
      opening.run(state, () => {
        void startNotificationScan(context.api, target.libraryId).then(() => service.apply('read', [note.id])).catch(error => {if (context.isCurrent()) captured.binding.home.effects.reportError(error);});
      });
      return;
    }
    opening.leave(state, current => {
      void service.apply('read', [note.id]);
      // Mark-read publishes optimistically; this already-claimed navigation
      // retains its original target and refuses a retired route/authority.
      if (!current()) return;
      if (target.view === 'home') go(() => router.navigate({to: '/'}));
      else if (target.view === 'settings') go(() => router.navigate(target.scope && target.section ? {to: '/settings', search: () => ({scope: target.scope, section: target.section, ...(target.row ? {row: target.row} : {})})} : {to: '/settings'}));
      else if (target.view === 'dashboard') go(() => router.navigate({to: '/dashboard'}));
      else if (target.view === 'library') go(() => router.navigate({to: '/library/$libraryId', params: {libraryId: target.id}}));
      else if (target.view === 'downloads') go(() => router.navigate({to: '/saved', search: {view: 'downloads'}}));
      else if (target.view === 'live') go(() => router.navigate({to: '/live', search: target.tab ? {tab: target.tab} : {}}));
      else if (target.view === 'search') go(() => router.navigate({to: '/search'}));
      else if (target.view === 'person') go(() => router.navigate({to: '/person/$personId', params: {personId: target.id}}));
      else go(() => router.navigate({to: '/item/$itemId', params: {itemId: target.id}}));
    });
  };
  const items = limit === undefined ? state.items : state.items.slice(0, limit);
  const seeAll = mode === 'popover' && (state.items.length > items.length || state.more)
    ? <Button variant="text" size="small" onClick={() => {if (opening?.owns(state)) go(() => router.navigate({to: '/notifications'}));}}>{t('notifications.seeAll')}</Button> : undefined;
  const panel = <NotificationsPanel snapshot={state} pages={mode === 'page'} footer={seeAll} onAudience={audience => {if (state.audiences.includes(audience)) opening?.run(state, () => {void service.load(audience, state.view);});}}
    onView={view => {opening?.run(state, () => {void service.load(state.audience, view);});}} onReadAll={() => {if (state.phase === 'ready' && state.revision > 0 && !state.busy) opening?.run(state, () => {void service.readAll();});}} onMore={() => {if (state.more && !state.busy) opening?.run(state, () => {void service.more();});}}>
    {state.phase === 'error' ? <StateView variant="compact" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('action.tryAgain'), onPress: () => {opening?.run(state, () => {void service.load(state.audience, state.view);});}}} secondary={{id: 'home', label: t('next.states.goHome'), onPress: close}} />
      : state.phase === 'ready' && !state.items.length ? <StateView variant="compact" icon="i-bell" title={t(state.view === 'unread' ? 'notifications.caughtUp' : state.view === 'archived' ? 'notifications.noneArchived' : 'notifications.none')} body={t('notifications.emptyBody')} />
        : items.map(note => {
          const words = noticeWords(note, defaultI18n);
          return <NotificationItem key={note.id} title={words.title} body={words.body} unread={!note.read} archived={note.archived} layout={phone ? 'phone' : 'desktop'}
            when={[{id: 'when', role: 'context', text: defaultI18n.relativeTime(note.createdAt)}]} actions={note.actions.map((action, index) => ({id: String(index), label: action.label,
              disabled: !notificationDestination(action, context.facts.identity.libraries, context.facts.identity.capabilities?.manageServer === true, note), onAction: () => open(note, action)}))}
            onMarkRead={() => apply('read', note)} onArchive={() => apply('archive', note)} onMoveToInbox={() => apply('unarchive', note)} />;
        })}
  </NotificationsPanel>;
  if (!opening) return null;
  return <>{panel}{report && <OwnFeedbackReport api={context.api} reportId={report} onClose={() => setReport(undefined)} />}</>;
}
export default defineWebScreen({id: 'notifications', component: NotificationsScreen, shellPopover: NotificationsPopover});
