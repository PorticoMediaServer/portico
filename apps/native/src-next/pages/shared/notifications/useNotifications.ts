import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {noticeWords, type Notification, type NotificationAction} from '../../../../../../packages/client-core/src/notifications';
import type {InboxAction, InboxService, InboxView} from '../../../../../../packages/client-core/src/inbox';
import {recoverEventState} from '../../../platform/event-resync';
import type {IconName} from '../../../kit/Icon';
import type {MenuEntry} from '../../../kit/overlays';
import {nativeMenuRouteAvailable} from '../../../shell/account';
import {sameAuthority, type ShellDestination} from '../../../shell/model';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRoute} from '../../../shell/routes';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {notificationCommand, notificationRoute, startNotificationScan, type NotificationCommandTarget} from '../../../screens/notifications/notification-route';

type Retirement = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Error | undefined;
}>;

/** The exact layout that drew these controls. Callbacks hold this object, never the
 * newest navigation/service state. The live flag is owned by this lease alone and is
 * closed by the layout cleanup, so an unmounted or replaced layout cannot act. */
type NotificationsLease = Readonly<{
  route: NativeShellRoute;
  revision: number;
  authority: ReturnType<NativeShellBootstrapContext['authority']>;
  origin: ShellDestination;
  profile: InboxService;
  retirement: Retirement;
  live: {value: boolean};
}>;

export type NotificationsPageProps = {
  context: NativeShellBootstrapContext; route: NativeShellRoute; runtime: NativeShellRuntime;
  profileService: InboxService; retirement: Retirement;
};

export function useNotifications({context, route, runtime, profileService, retirement}: NotificationsPageProps) {
  const {t} = runtime.text;
  const profile = useSyncExternalStore(profileService.subscribe, profileService.getSnapshot);
  const retired = useSyncExternalStore(retirement.subscribe, retirement.getSnapshot);
  const [view, setView] = useState<InboxView>('unread');
  const [chosen, setChosen] = useState<Notification>(), [report, setReport] = useState<string>();
  const [lease, setLease] = useState<NotificationsLease>();

  // Admission and retirement belong to the committed layout, not to render. The
  // state-published lease is what every callback below captures.
  useLayoutEffect(() => {
    const live = {value: true};
    const next = Object.freeze({
      route, revision: context.navigation.getSnapshot().revision,
      authority: Object.freeze({...context.authority()}),
      origin: context.navigation.getSnapshot().destination,
      profile: profileService, retirement, live,
    });
    setLease(next);
    return () => {
      live.value = false;
      setLease(current => current === next ? undefined : current);
    };
  }, [context, profileService, retirement, route]);

  // The captured route/revision/authority and the lease's own live flag, without the
  // retirement check. Only the retirement fallback's Back uses this; it must still be
  // unable to navigate once this exact layout is gone or replaced.
  const routeLive = () => !!lease && lease.live.value
    && sameAuthority(lease.authority, context.authority())
    && context.navigation.getSnapshot().revision === lease.revision
    && context.navigation.getSnapshot().route === lease.route;
  const current = () => !!lease && routeLive() && lease.retirement.getSnapshot() === undefined;

  // The committed lease and the drawn tab are the one load trigger for each feed: a lease
  // commit loads the view it was committed with, and a tab press loads the new view once it
  // commits. changeView only draws the tab, so the tab is read once, never twice.
  useEffect(() => {
    if (!lease || retired) return;
    void lease.profile.load('profile', view);
  }, [lease, retired, view]);
  useEffect(() => {
    if (!lease || retired) return;
    // OW-5: a change hint rereads the inbox quietly, skipping the echo of its own write and
    // waiting for a write that is still out rather than cancelling it.
    const refresh = (event: {revision?: string}) => {
      // The exact `current()` test, inlined: this subscription may only load while the layout
      // it was registered for is still the committed one. It never borrows a newer authority.
      const live = lease.live.value
        && sameAuthority(lease.authority, context.authority())
        && context.navigation.getSnapshot().revision === lease.revision
        && context.navigation.getSnapshot().route === lease.route
        && lease.retirement.getSnapshot() === undefined;
      if (!live) return;
      lease.profile.refresh(event.revision);
    };
    const recover = async (signal: AbortSignal) => {
      const live = lease.live.value && sameAuthority(lease.authority, context.authority())
        && context.navigation.getSnapshot().revision === lease.revision && context.navigation.getSnapshot().route === lease.route
        && lease.retirement.getSnapshot() === undefined;
      if (!live || signal.aborted) return;
      await lease.profile.load('profile', view);
      if (signal.aborted || !lease.live.value) return;
      const error = lease.profile.getSnapshot().error;
      if (error) throw error;
    };
    const offs = [context.events.on('notification.changed', refresh),
      recoverEventState(context.events, ({signal}) => recover(signal))];
    return () => {for (const off of offs) off();};
  }, [context, lease, retired, view]);

  const close = () => {if (routeLive() && context.navigation.back(context.authority())) setChosen(undefined);};
  const admitted = !!lease && lease.profile === profileService && lease.retirement === retirement;
  const hasLiveTV = admitted && runtime.getSnapshot().hasLiveTV;
  const service = lease?.profile ?? profileService;
  const viewLive = (service: InboxService, viewAt: InboxView) => current()
    && service === lease?.profile
    && service.getSnapshot().view === viewAt;
  const noticeLive = (service: InboxService, viewAt: InboxView, notice: Notification) => {
    const snapshot = service.getSnapshot();
    return viewLive(service, viewAt) && snapshot.items.some(item =>
      item.id === notice.id && item.audience === notice.audience && item.revision === notice.revision);
  };

  const changeView = (next: string) => {
    if (!current() || service.getSnapshot().view !== view) return;
    setView(next as InboxView);
  };
  const reload = () => {
    if (!current() || service.getSnapshot().view !== view) return;
    void service.load('profile', view);
  };
  const words = (notice: Notification) => noticeWords(notice, {has: defaultI18n.has, t: runtime.text.t});
  const unread = profile.counts.unread;
  const busy = profile.busy;
  const error = profile.error;
  const loading = profile.phase === 'loading';
  const ready = profile.phase === 'ready';
  const items = profile.items;
  const sections = [
    {id: 'profile', title: t('notifications.audience.profile'), service, state: profile},
  ].filter(section => section.state.items.length > 0);
  type Section = (typeof sections)[number];
  type Row = Readonly<{kind: 'heading'; key: string; section: Section}> | Readonly<{kind: 'more'; key: string; section: Section}> | Readonly<{kind: 'notice'; key: string; section: Section; notice: Notification; first: boolean; last: boolean}>;
  const rows: Row[] = sections.flatMap(section => [
    {kind: 'heading' as const, key: `heading:${section.id}`, section},
    ...section.state.items.map((notice, index) => ({kind: 'notice' as const, key: `notice:${section.id}:${notice.id}`, section, notice,
      first: index === 0, last: index === section.state.items.length - 1})),
    ...(section.state.more ? [{kind: 'more' as const, key: `more:${section.id}`, section}] : []),
  ]);

  const routeFor = (action: NotificationAction, notice: Notification) => notificationRoute(action, hasLiveTV, notice);
  const routeAvailable = (targetRoute: NativeShellRoute) =>
    targetRoute.kind === 'shell' && targetRoute.destination.kind === 'home' ? true : nativeMenuRouteAvailable(targetRoute);
  const openAction = (notice: Notification, targetRoute: NativeShellRoute, viewAt: InboxView) => {
    if (!noticeLive(service, viewAt, notice) || !routeAvailable(targetRoute)) return;
    // Mark read while this original notice and route lease are still current, then
    // navigate only if that is still true. The read is an independent command, not
    // a promise carried by the route switch.
    void service.apply('read', [notice.id]).catch(runtime.reportError);
    if (!current()) return;
    context.navigation.open(targetRoute, context.authority());
  };
  // OW-1: Dismiss archives the notice; Scan again starts the library's scan and marks the notice read.
  const runCommand = (notice: Notification, command: NotificationCommandTarget, viewAt: InboxView) => {
    if (!noticeLive(service, viewAt, notice)) return;
    if (command.kind === 'archive') {void service.apply('archive', [notice.id]).catch(runtime.reportError); return;}
    if (command.kind === 'report') {setReport(command.reportId); void service.apply('read', [notice.id]).catch(runtime.reportError); return;}
    void startNotificationScan(context.api, command.libraryId).then(() => service.apply('read', [notice.id])).catch(runtime.reportError);
  };
  const actionsFor = (notice: Notification, viewAt: InboxView) => notice.actions.flatMap((action, index) => {
    const command = notificationCommand(action, notice);
    if (command) return [{id: String(index), label: action.label, onPress: () => runCommand(notice, command, viewAt)}];
    const targetRoute = routeFor(action, notice);
    return targetRoute && routeAvailable(targetRoute) ? [{id: String(index), label: action.label, onPress: () => openAction(notice, targetRoute, viewAt)}] : [];
  });
  const apply = (notice: Notification, action: InboxAction, viewAt: InboxView) => {
    setChosen(undefined);
    if (!noticeLive(service, viewAt, notice)) return;
    void service.apply(action, [notice.id]).catch(runtime.reportError);
  };
  const markAll = () => {
    if (!current()) return;
    if (viewLive(service, view)) void service.readAll().catch(runtime.reportError);
  };
  const chosenWords = chosen ? words(chosen) : undefined;
  const menu: readonly MenuEntry[] = chosen ? [
    ...(view === 'archived' ? [] : [{id: chosen.read ? 'unread' : 'read', label: t(chosen.read ? 'notifications.markUnread' : 'notifications.markRead'), icon: (chosen.read ? 'i-eyeoff' : 'i-check') as IconName, onSelect: () => apply(chosen, chosen.read ? 'unread' : 'read', view)}]),
    {id: chosen.archived ? 'unarchive' : 'archive', label: t(chosen.archived ? 'notifications.unarchive' : 'notifications.archive'), icon: 'i-archive' as IconName, onSelect: () => apply(chosen, chosen.archived ? 'unarchive' : 'archive', view)},
  ] : [];
  const emptyTitle = view === 'unread' ? t('notifications.caughtUp') : view === 'archived' ? t('notifications.noneArchived') : t('notifications.none');
  const backLabel = lease?.origin.kind === 'settings' ? t('next.shell.settings') : t('action.back');

  return {
    admitted, retired, view, unread, busy, loading, ready, error, items, rows, emptyTitle, backLabel, chosen, chosenWords, menu, report,
    close, markAll, changeView, reload, words, actionsFor,
    back: () => {if (current()) close();},
    closeMenu: () => setChosen(undefined),
    closeReport: () => setReport(undefined),
    showMenu: (notice: Notification) => {if (noticeLive(service, view, notice)) setChosen(notice);},
    loadMore: (row: Extract<Row, {kind: 'more'}>) => {if (viewLive(row.section.service, view)) void row.section.service.more().catch(runtime.reportError);},
  };
}
