import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {noticeWords, type Notification, type NotificationAction} from '@core/notifications.ts';
import {InboxService, type InboxAction, type InboxView} from '@core/inbox.ts';
import type {NotificationAudience} from '@core/notifications.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {NotificationItem} from '../../kit/shell/AccountSurfaces';
import {NotificationsPanel, NotificationsPanelState} from '../../kit/shell/NotificationsPanel';
import {Popover} from '../../kit/overlays';
import {ShellScrim} from '../../kit/shell/ShellScrim';
import {StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {Button, Tabs} from '../../kit/controls';
import {PushedNavBar} from '../../kit/shell/SettingsSurfaces';
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
  const captured = useProtectedWebScreenContext(), width = useShellWidth(), phone = width < 900, narrow = width < 600;
  if (!captured) return null;
  if (narrow) return <PhoneNotifications captured={captured}/>;
  return phone ? <NotificationsFeed captured={captured} mode="page" /> : <div className={styles.page}><NotificationsFeed captured={captured} mode="page" /></div>;
}

const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
/** phone-app 21 at phone width: "‹ Settings", the title and Mark all read; one Unread / All / Archived row; then the
 * notices "For you" and, for an owner, "Server · Owner only". The Server section is its own inbox, made on this page
 * for owners only and gone when the page goes; the bell and the desktop feed keep the shell's one inbox. */
function PhoneNotifications({captured}: {captured: Captured}) {
  const {context} = captured, shared = context.inbox, sharedState = useSyncExternalStore(shared.subscribe, shared.getSnapshot);
  const [view, setView] = useState<InboxView>('unread');
  const owner = context.facts.identity.viewer.role === 'owner' && sharedState.audiences.includes('account-admin');
  const server = useMemo(() => owner ? new InboxService(context.api, undefined, {queries: context.freshness.queries}) : undefined, [owner, context]);
  useEffect(() => () => {server?.dispose();}, [server]);
  const serverState = useSyncExternalStore(server?.subscribe ?? noSubscribe, server?.getSnapshot ?? noSnapshot);
  // Each inbox takes the press itself: a busy one (a view switch still reading, a change out) holds
  // it until it is free rather than dropping it, and one with no page it may change refuses it.
  const readAll = () => {
    for (const [inbox, snapshot] of [[shared, sharedState], [server, serverState]] as const) {
      if (inbox && snapshot && snapshot.phase !== 'idle') void inbox.readAll();
    }
  };
  const back = () => {if (!context.isCurrent()) return; if (window.history.length > 1) router.history.back(); else void router.navigate({to: '/settings'});};
  const unread = sharedState.unread;
  return <div className={styles.phonePage}>
    <PushedNavBar safeArea={false} backLabel={t('next.shell.settings')} title={t('notifications.title')} onBack={back}
      trailing={<Button variant="text" size="small" className={styles.markAll} onClick={readAll}>{t('notifications.markAllRead')}</Button>}/>
    <Tabs phone className={styles.phoneViews} value={view} aria-label={t('web.notifications.show')}
      onValueChange={value => {if (value === 'unread' || value === 'all' || value === 'archived') setView(value);}}
      items={(['unread', 'all', 'archived'] as const).map(value => ({value, label: value === 'unread' && unread > 0
        ? <>{t('notifications.view.unread')}<span className={styles.viewCount}>{defaultI18n.number(unread)}</span></> : t(`notifications.view.${value}`)}))}/>
    <NotificationsFeed captured={captured} mode="page" section={{audience: 'profile', view, title: t('notifications.audience.profile')}}/>
    {server && <NotificationsFeed captured={captured} mode="page" service={server}
      section={{audience: 'account-admin', view, title: <>{t('notifications.audience.admin')}<span>{t('next.shell.ownerOnly')}</span></>}}/>}
  </div>;
}

/** desktop-search "Notifications": the bell opens a panel over the page, as today. It is a shell
 * popover, not a route, so opening and closing it never remounts the page under it. */
function NotificationsPopover({anchorRef, onClose}: ShellPopoverProps) {
  const captured = useProtectedWebScreenContext();
  if (!captured) return null;
  // desktop-search Notifications: like the avatar and Activity menus, the page dims behind the panel.
  return <><ShellScrim open context="activity" /><Popover open title={t('notifications.title')} accessibilityLabel={t('notifications.title')} anchorRef={anchorRef} returnFocusRef={anchorRef}
    onOpenChange={value => {if (!value) onClose();}} modal width={452} sideOffset={10}>
    <NotificationsFeed captured={captured} mode="popover" limit={BELL_NOTICES} onDone={onClose} />
  </Popover></>;
}

/** One section of phone-app 21: an audience at the page's view, drawn as a heading and its notices. */
type FeedSection = Readonly<{audience: NotificationAudience; view: InboxView; title: ReactNode}>;
function NotificationsFeed({captured, mode, limit, onDone, service: given, section}: {captured: Captured; mode: 'page' | 'popover'; limit?: number; onDone?: () => void;
  /** The shell's inbox unless a page brings its own (the phone page's Server section). */
  service?: InboxService; section?: FeedSection}) {
  useShellFacts(captured.context);
  const {context} = captured, service = given ?? context.inbox, state = useSyncExternalStore(service.subscribe, service.getSnapshot);
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
  // A section reads its own audience at the page's view, once per audience/view; otherwise the drawn view.
  const sectionRead = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!opening) return;
    if (!section) {opening.initialize(state); return;}
    const key = `${section.audience}:${section.view}`;
    if (sectionRead.current === key && state.audience === section.audience && state.view === section.view) return;
    sectionRead.current = key;
    opening.run(state, () => {void service.load(section.audience, section.view);});
  }, [opening, state, section, service]);
  // OW-5: an open page or panel follows the inbox. A change hint, or a stream resync, rereads
  // the view on screen quietly; the shell refreshes the badge on its own.
  useEffect(() => {
    if (!opening) return;
    const refresh = (revision?: string) => {if (opening.current()) service.refresh(revision);};
    const offs = [context.events.on('notification.changed', event => refresh(event.revision)), context.events.onResync(() => refresh())];
    return () => {for (const off of offs) off();};
  }, [opening, service, context.events]);
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
  const body = <>
    {state.phase === 'error' ? <NotificationsPanelState><StateView variant="compact" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('action.tryAgain'), onPress: () => {opening?.run(state, () => {void service.load(state.audience, state.view);});}}} secondary={{id: 'home', label: t('next.states.goHome'), onPress: close}} /></NotificationsPanelState>
      : state.phase === 'ready' && !state.items.length ? <NotificationsPanelState><StateView variant="compact" icon="i-bell" title={t(state.view === 'unread' ? 'notifications.caughtUp' : state.view === 'archived' ? 'notifications.noneArchived' : 'notifications.none')} body={t('notifications.emptyBody')} /></NotificationsPanelState>
        : items.map(note => {
          const words = noticeWords(note, defaultI18n);
          return <NotificationItem key={note.id} title={words.title} body={words.body} unread={!note.read} archived={note.archived} layout={phone ? 'phone' : 'desktop'}
            when={[{id: 'when', role: 'context', text: defaultI18n.relativeTime(note.createdAt)}]} actions={note.actions.map((action, index) => ({id: String(index), label: action.label,
              disabled: !notificationDestination(action, context.facts.identity.libraries, context.facts.identity.capabilities?.manageServer === true, note), onAction: () => open(note, action)}))}
            onMarkRead={() => apply('read', note)} onArchive={() => apply('archive', note)} onMoveToInbox={() => apply('unarchive', note)} />;
        })}
  </>;
  const panel = <NotificationsPanel snapshot={state} pages={mode === 'page'} footer={seeAll} onAudience={audience => {if (state.audiences.includes(audience)) opening?.run(state, () => {void service.load(audience, state.view);});}}
    onView={view => {opening?.run(state, () => {void service.load(state.audience, view);});}} onReadAll={() => {if (state.phase === 'ready' && state.revision > 0) opening?.run(state, () => {void service.readAll();});}} onMore={() => {if (state.more && !state.busy) opening?.run(state, () => {void service.more();});}}>
    {body}
  </NotificationsPanel>;
  if (!opening) return null;
  if (section) return <section className={styles.phoneSection} aria-label={typeof section.title === 'string' ? section.title : undefined}>
    <h2>{section.title}</h2>{body}{state.more && <Button variant="text" size="small" onClick={() => {if (state.more) opening.run(state, () => {void service.more();});}}>{t('web.pager.next')}</Button>}
    {report && <OwnFeedbackReport api={context.api} reportId={report} onClose={() => setReport(undefined)} />}</section>;
  return <>{panel}{report && <OwnFeedbackReport api={context.api} reportId={report} onClose={() => setReport(undefined)} />}</>;
}
export default defineWebScreen({id: 'notifications', component: NotificationsScreen, shellPopover: NotificationsPopover});
