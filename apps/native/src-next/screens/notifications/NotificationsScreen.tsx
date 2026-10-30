import {recoverEventState} from '../../platform/event-resync';
import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {noticeWords, type Notification, type NotificationAction} from '../../../../../packages/client-core/src/notifications';
import type {InboxAction, InboxService, InboxView} from '../../../../../packages/client-core/src/inbox';
import {Button, Tabs} from '../../kit/controls';
import {Icon, type IconName} from '../../kit/Icon';
import {BottomSheet, Menu, type MenuEntry} from '../../kit/overlays';
import {NotificationItem} from '../../kit/shell';
import {Quiet, StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {nativeMenuRouteAvailable} from '../../shell/account';
import {sameAuthority, type ShellDestination} from '../../shell/model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import {StandardBackdrop} from '../../kit/artwork';

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
  admin: InboxService;
  retirement: Retirement;
  live: {value: boolean};
}>;

/** phone-app 9/21: the server's inbox, split by the audience envelopes it returns.
 * The profile feed is always this viewer's. The account-admin feed is requested only
 * for a local owner, the same rule the server enforces. Every word is either the
 * server's own notice or the catalogue; no notice is made up on the device. */
export function NativeNotificationsScreen({context, route, runtime, profileService, adminService, retirement}: {
  context: NativeShellBootstrapContext;
  route: NativeShellRoute;
  runtime: NativeShellRuntime;
  profileService: InboxService;
  adminService: InboxService;
  retirement: Retirement;
}) {
  const theme = useTheme(), t = runtime.text.t, insets = useSafeAreaInsets();
  const profile = useSyncExternalStore(profileService.subscribe, profileService.getSnapshot);
  const admin = useSyncExternalStore(adminService.subscribe, adminService.getSnapshot);
  const retired = useSyncExternalStore(retirement.subscribe, retirement.getSnapshot);
  const [view, setView] = useState<InboxView>('unread');
  const [chosen, setChosen] = useState<Notification>();
  const [lease, setLease] = useState<NotificationsLease>();

  // Admission and retirement belong to the committed layout, not to render. The
  // state-published lease is what every callback below captures.
  useLayoutEffect(() => {
    const live = {value: true};
    const next = Object.freeze({
      route, revision: context.navigation.getSnapshot().revision,
      authority: Object.freeze({...context.authority()}),
      origin: context.navigation.getSnapshot().destination,
      profile: profileService, admin: adminService, retirement, live,
    });
    setLease(next);
    return () => {
      live.value = false;
      setLease(current => current === next ? undefined : current);
    };
  }, [adminService, context, profileService, retirement, route]);

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
    if (!lease || retired || context.account.account.role !== 'owner') return;
    void lease.admin.load('account-admin', view);
  }, [context, lease, retired, view]);
  useEffect(() => {
    if (!lease || retired) return;
    const refresh = () => {
      // The exact `current()` test, inlined: this subscription may only load while the layout
      // it was registered for is still the committed one. It never borrows a newer authority.
      const live = lease.live.value
        && sameAuthority(lease.authority, context.authority())
        && context.navigation.getSnapshot().revision === lease.revision
        && context.navigation.getSnapshot().route === lease.route
        && lease.retirement.getSnapshot() === undefined;
      if (!live) return;
      void lease.profile.load('profile', view);
      if (context.account.account.role === 'owner') void lease.admin.load('account-admin', view);
    };
    const recover = async (service: InboxService, audience: 'profile' | 'account-admin', signal: AbortSignal) => {
      const live = lease.live.value && sameAuthority(lease.authority, context.authority())
        && context.navigation.getSnapshot().revision === lease.revision && context.navigation.getSnapshot().route === lease.route
        && lease.retirement.getSnapshot() === undefined;
      if (!live || signal.aborted) return;
      await service.load(audience, view);
      if (signal.aborted || !lease.live.value) return;
      const error = service.getSnapshot().error;
      if (error) throw error;
    };
    const offs = [context.events.on('notification.changed', refresh),
      recoverEventState(context.events, ({signal}) => recover(lease.profile, 'profile', signal))];
    if (context.account.account.role === 'owner') offs.push(recoverEventState(context.events, ({signal}) => recover(lease.admin, 'account-admin', signal)));
    return () => {for (const off of offs) off();};
  }, [context, lease, retired, view]);

  const close = () => {if (routeLive() && context.navigation.back(context.authority())) setChosen(undefined);};
  if (retired) return <NotificationsFrame testID="native-notifications-retired" title={t('next.shell.notifications')} onClose={close}>
    <StateView icon="i-warn" title={t('notifications.error.load')} primaryAction={{id: 'back', label: t('action.back'), onPress: () => {if (routeLive()) context.navigation.back(context.authority());}}}/>
  </NotificationsFrame>;
  if (!lease || lease.profile !== profileService || lease.admin !== adminService || lease.retirement !== retirement) return <NotificationsFrame testID="native-notifications-pending" title={t('next.shell.notifications')} onClose={close}/>;

  const hasLiveTV = runtime.getSnapshot().hasLiveTV;
  const adminRequested = context.account.account.role === 'owner';
  const viewLive = (service: InboxService, viewAt: InboxView) => current()
    && (service === lease.profile || service === lease.admin)
    && service.getSnapshot().view === viewAt;
  const noticeLive = (service: InboxService, viewAt: InboxView, notice: Notification) => {
    const snapshot = service.getSnapshot();
    return viewLive(service, viewAt) && snapshot.items.some(item =>
      item.id === notice.id && item.audience === notice.audience && item.revision === notice.revision);
  };
  const serviceFor = (notice: Notification) => notice.audience === 'account-admin' ? lease.admin : lease.profile;

  const changeView = (next: string) => {
    if (!current() || lease.profile.getSnapshot().view !== view) return;
    setView(next as InboxView);
  };
  const reload = () => {
    if (!current() || lease.profile.getSnapshot().view !== view) return;
    void lease.profile.load('profile', view);
    if (adminRequested) void lease.admin.load('account-admin', view);
  };
  const words = (notice: Notification) => noticeWords(notice, {has: defaultI18n.has, t: runtime.text.t});
  const adminVisible = adminRequested && admin.phase === 'ready' && admin.audiences.includes('account-admin');
  const unread = profile.counts.unread + (adminVisible ? admin.counts.unread : 0);
  const busy = profile.busy || (adminVisible && admin.busy);
  const error = profile.error ?? (adminRequested ? admin.error : undefined);
  const loading = profile.phase === 'loading' || (adminRequested && admin.phase === 'loading');
  const ready = profile.phase === 'ready' && (!adminRequested || admin.phase === 'ready');
  const items = [...profile.items, ...(adminVisible ? admin.items : [])];
  const sections = [
    {id: 'profile', title: t('notifications.audience.profile'), tag: undefined, service: lease.profile, state: profile},
    ...(adminVisible ? [{id: 'admin', title: t('notifications.audience.admin'), tag: t('next.shell.ownerOnly'), service: lease.admin, state: admin}] : []),
  ].filter(section => section.state.items.length > 0);
  type Section = (typeof sections)[number];
  type Row = Readonly<{kind: 'heading'; key: string; section: Section}> | Readonly<{kind: 'more'; key: string; section: Section}> | Readonly<{kind: 'notice'; key: string; section: Section; notice: Notification; first: boolean; last: boolean}>;
  const rows: Row[] = sections.flatMap(section => [
    {kind: 'heading' as const, key: `heading:${section.id}`, section},
    ...section.state.items.map((notice, index) => ({kind: 'notice' as const, key: `notice:${section.id}:${notice.id}`, section, notice,
      first: index === 0, last: index === section.state.items.length - 1})),
    ...(section.state.more ? [{kind: 'more' as const, key: `more:${section.id}`, section}] : []),
  ]);

  const routeFor = (action: NotificationAction): NativeShellRoute | undefined => {
    if (action.kind === 'command') return action.command === 'open-download' ? {kind: 'downloads'} : undefined;
    const targetView = action.target?.view, libraryId = action.target?.libraryId, entityId = action.target?.entityId;
    switch (targetView) {
      case 'home': return {kind: 'shell', destination: {kind: 'home'}};
      case 'library': return libraryId ? {kind: 'shell', destination: {kind: 'library', libraryId}} : undefined;
      case 'live': return hasLiveTV ? {kind: 'shell', destination: {kind: 'live'}} : undefined;
      case 'search': return {kind: 'shell', destination: {kind: 'search'}};
      // Root33: a preparation-ready notice only points at the real Downloads
      // surface. It never claims the file is on this device or locally playable.
      case 'downloads': return {kind: 'downloads'};
      case 'recordings':
      case 'recording': return {kind: 'recordings'};
      case 'admin-feedback': return {kind: 'server-feedback'};
      case 'settings': return {kind: 'shell', destination: {kind: 'settings'}};
      case 'person': return entityId ? {kind: 'person', personId: entityId} : undefined;
      // A view this build has no real surface for stays out rather than guessing.
      default: return undefined;
    }
  };
  const routeAvailable = (targetRoute: NativeShellRoute) =>
    targetRoute.kind === 'shell' && targetRoute.destination.kind === 'home' ? true : nativeMenuRouteAvailable(targetRoute);
  const openAction = (notice: Notification, targetRoute: NativeShellRoute, viewAt: InboxView) => {
    const service = serviceFor(notice);
    if (!noticeLive(service, viewAt, notice) || !routeAvailable(targetRoute)) return;
    // Mark read while this original notice and route lease are still current, then
    // navigate only if that is still true. The read is an independent command, not
    // a promise carried by the route switch.
    void service.apply('read', [notice.id]).catch(runtime.reportError);
    if (!current()) return;
    context.navigation.open(targetRoute, context.authority());
  };
  const actionsFor = (notice: Notification, viewAt: InboxView) => notice.actions.flatMap((action, index) => {
    const targetRoute = routeFor(action);
    return targetRoute && routeAvailable(targetRoute) ? [{id: String(index), label: action.label, onPress: () => openAction(notice, targetRoute, viewAt)}] : [];
  });
  const apply = (notice: Notification, action: InboxAction, viewAt: InboxView) => {
    setChosen(undefined);
    const service = serviceFor(notice);
    if (!noticeLive(service, viewAt, notice)) return;
    void service.apply(action, [notice.id]).catch(runtime.reportError);
  };
  const markAll = () => {
    if (!current()) return;
    if (viewLive(lease.profile, view)) void lease.profile.readAll().catch(runtime.reportError);
    if (adminVisible && viewLive(lease.admin, view)) void lease.admin.readAll().catch(runtime.reportError);
  };
  const chosenWords = chosen ? words(chosen) : undefined;
  const menu: readonly MenuEntry[] = chosen ? [
    ...(view === 'archived' ? [] : [{id: chosen.read ? 'unread' : 'read', label: t(chosen.read ? 'notifications.markUnread' : 'notifications.markRead'), icon: (chosen.read ? 'i-eyeoff' : 'i-check') as IconName, onSelect: () => apply(chosen, chosen.read ? 'unread' : 'read', view)}]),
    {id: chosen.archived ? 'unarchive' : 'archive', label: t(chosen.archived ? 'notifications.unarchive' : 'notifications.archive'), icon: 'i-archive' as IconName, onSelect: () => apply(chosen, chosen.archived ? 'unarchive' : 'archive', view)},
  ] : [];
  const emptyTitle = view === 'unread' ? t('notifications.caughtUp') : view === 'archived' ? t('notifications.noneArchived') : t('notifications.none');
  const backLabel = lease.origin.kind === 'settings' ? t('next.shell.settings') : t('action.back');

  const header = <View style={{height: 56, paddingHorizontal: 10, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>
      {theme.platform === 'phone' && <View accessible={false} pointerEvents="none" style={{position: 'absolute', top: 6, alignSelf: 'center', width: 38, height: 5, borderRadius: 3, backgroundColor: theme.colors.lineStrong}}/>}
      <Button testID="notifications-back" variant="text" size="small" icon="i-left" accessibilityLabel={backLabel}
        onPress={() => {if (!current()) return; if (context.navigation.back(context.authority())) setChosen(undefined);}} style={{minHeight: 38, paddingHorizontal: 6, marginLeft: -4}}
        textStyle={shellType(16, 600)}>{backLabel}</Button>
      <Text accessibilityRole="header" pointerEvents="none" style={[shellType(17, 700), {position: 'absolute', left: 96, right: 96, textAlign: 'center', color: theme.colors.text}]}>{t('next.shell.notifications')}</Text>
      {view !== 'archived' && unread > 0 ? <Button testID="notifications-mark-all" variant="text" size="small" disabled={busy} onPress={markAll}
        style={{minHeight: 38, paddingHorizontal: 6, marginRight: -4}} textStyle={shellType(15, 700)}>{t('notifications.markAllRead')}</Button> : <View/>}
    </View>;
  return <NotificationsFrame testID="native-notifications" title={t('next.shell.notifications')} onClose={close} header={header}>
    <Tabs testID="notifications-views" value={view} onValueChange={changeView} items={[
      {value: 'unread', label: t('notifications.view.unread'), count: unread > 0 ? defaultI18n.number(unread) : undefined},
      {value: 'all', label: t('notifications.view.all')},
      {value: 'archived', label: t('notifications.view.archived')},
    ]}/>
    {/* LR09: one keyed row per heading, notice and Load more, on a viewport list; the
        service keeps the fetched history, the list mounts only the rows in view. */}
    <FlatList testID="notifications-list" style={{flex: 1}} data={rows} keyExtractor={row => row.key} initialNumToRender={12} windowSize={7}
      contentContainerStyle={{paddingBottom: theme.platform === 'phone' ? 0 : Math.max(24, insets.bottom + 16), paddingTop: 4}}
      ListHeaderComponent={<>
        {error !== undefined && items.length > 0 && <StateView tone="warning" title={error} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: reload}} style={{paddingTop: 8, paddingBottom: 8}}/>}
        {loading && !items.length && <Quiet><StateView icon="i-bell" title={t('notifications.loading')}/></Quiet>}
        {!loading && !items.length && error !== undefined && <StateView icon="i-warn" title={t('notifications.error.load')} body={error} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: reload}}/>}
        {ready && !items.length && error === undefined && <StateView icon={view === 'archived' ? 'i-archive' : 'i-bell'} title={emptyTitle} body={t('notifications.emptyBody')}/>}
      </>}
      renderItem={({item: row}) => {
        if (row.kind === 'heading') return <View testID={`notifications-section-${row.section.id}`} style={{flexDirection: 'row', alignItems: 'baseline', gap: 8, paddingHorizontal: 20, marginTop: 12, marginBottom: 8}}>
          <Text style={[shellType(13, 700), {color: theme.colors.textTertiary}]}>{row.section.title}</Text>
          {row.section.tag && <Text style={[shellType(12, 700), {color: theme.colors.textTertiary}]}>{row.section.tag}</Text>}
        </View>;
        if (row.kind === 'more') return <View style={{alignItems: 'center', marginTop: 10}}><Button testID={`notifications-more-${row.section.id}`} variant="glass" size="small" loading={row.section.state.busy}
          onPress={() => {if (!viewLive(row.section.service, view)) return; void row.section.service.more().catch(runtime.reportError);}}>{t('notifications.loadMore')}</Button></View>;
        const {notice, first, last, section} = row, message = words(notice), radius = {
          borderTopLeftRadius: first ? 14 : 0, borderTopRightRadius: first ? 14 : 0, borderBottomLeftRadius: last ? 14 : 0, borderBottomRightRadius: last ? 14 : 0};
        // Each row draws its own slice of the section's raised card (and the admin outline).
        return <View style={{marginHorizontal: 16, overflow: 'hidden', backgroundColor: theme.colors.raised, ...radius}}>
          <NotificationItem testID={`notification-${notice.id}`} separator={!first} notification={notice} title={message.title} body={message.body} when={defaultI18n.relativeTime(notice.createdAt)}
            subject={<Icon name={sourceIcon(notice.source)} size={18} color={notice.severity === 'critical' ? theme.colors.record : notice.severity === 'warning' ? theme.colors.amber : theme.colors.textSecondary}/>}
            actions={actionsFor(notice, view)} menuLabel={t('next.shell.native.moreOptions')} onMenu={() => {const service = serviceFor(notice); if (!noticeLive(service, view, notice)) return; setChosen(notice);}} style={{opacity: notice.read && view !== 'archived' ? .7 : 1}}/>
          {section.id === 'admin' && <View pointerEvents="none" accessible={false} style={{position: 'absolute', inset: 0, ...radius, borderColor: 'rgba(92,179,226,.28)',
            borderLeftWidth: 1, borderRightWidth: 1, borderTopWidth: first ? 1 : 0, borderBottomWidth: last ? 1 : 0}}/>}
        </View>;
      }}/>
    <Menu testID="notification-actions" open={chosen !== undefined} onOpenChange={open => {if (!open) setChosen(undefined);}} presentation="sheet"
      title={chosenWords?.title} description={chosenWords?.body} items={menu}/>
  </NotificationsFrame>;
}

/** The transparent modal keeps the retained origin visible and inert behind it.
 * Header gestures, Back, escape and screen-reader containment use the shared host. */
function NotificationsFrame({children, header, title, testID, onClose}: {children?: React.ReactNode; header?: React.ReactNode; title: string; testID: string; onClose: () => void}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), window = useWindowDimensions();
  if (theme.platform !== 'phone') return <View testID={testID} style={{flex: 1, paddingTop: insets.top + 14}}><StandardBackdrop/>{header}{children}</View>;
  return <BottomSheet testID={testID} title={title} open onOpenChange={open => {if (!open) onClose();}} accessibilityLabel={title}
    detents={[Math.max(1, window.height - 62)]} headerContent={header ?? <View style={{height: 56}}/>} scrollable={false} showClose={false} showGrabber={false}
    style={{backgroundColor: theme.colors.ink, borderTopLeftRadius: 16, borderTopRightRadius: 16, paddingTop: 0}}>
    {children}
  </BottomSheet>;
}

const sourceIcon = (source: string): IconName => source === 'downloads' ? 'i-dl'
  : source === 'dvr' ? 'i-rec'
    : source === 'feedback' ? 'i-bubble'
      : source === 'security' ? 'i-shield'
        : source === 'scan' ? 'i-refresh'
          : source === 'networking' ? 'i-wifi'
            : source === 'storage' ? 'i-disk' : 'i-bell';
