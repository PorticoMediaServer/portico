import React from 'react';
import {FlatList, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n, type I18n} from '../../../../../packages/i18n/src';
import type {useNotifications} from '../../pages/shared/notifications/useNotifications';
import {Button} from '../controls';
import {Icon, type IconName} from '../Icon';
import {Quiet, StateView} from '../states';
import {useTheme} from '../theme';
import {NotificationItem} from './AccountShell';

export function NotificationsList({model, t}: {model: ReturnType<typeof useNotifications>; t: I18n['t']}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const {rows, items, error, loading, ready, view, emptyTitle, reload, words, actionsFor} = model;
  return <FlatList testID="notifications-list" style={{flex: 1}} data={rows} keyExtractor={row => row.key} initialNumToRender={12} windowSize={7}
      contentContainerStyle={{paddingBottom: theme.platform === 'phone' ? 0 : insets.bottom + theme.space.md, paddingTop: theme.space.xs}}
      ListHeaderComponent={<>
        {error !== undefined && items.length > 0 && <StateView tone="warning" title={error} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: reload}} style={{paddingVertical: theme.space.sm}}/>}
        {loading && !items.length && <Quiet><StateView icon="i-bell" title={t('notifications.loading')}/></Quiet>}
        {!loading && !items.length && error !== undefined && <StateView icon="i-warn" title={t('notifications.error.load')} body={error} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: reload}}/>}
        {ready && !items.length && error === undefined && <StateView icon={view === 'archived' ? 'i-archive' : 'i-bell'} title={emptyTitle} body={t('notifications.emptyBody')}/>}
      </>}
      renderItem={({item: row}) => {
        if (row.kind === 'heading') return <View testID={`notifications-section-${row.section.id}`} style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.space.sm, paddingHorizontal: theme.gutter, marginVertical: theme.space.sm}}>
          <Text style={[theme.type.groupLabel, {color: theme.colors.textTertiary}]}>{row.section.title}</Text>
        </View>;
        if (row.kind === 'more') return <View style={{alignItems: 'center', marginTop: theme.space.sm}}><Button align="center" testID={`notifications-more-${row.section.id}`} variant="glass" size="small" loading={row.section.state.busy}
          onPress={() => model.loadMore(row)}>{t('notifications.loadMore')}</Button></View>;
        const {notice, first, last} = row, message = words(notice), radius = {
          borderTopLeftRadius: first ? theme.radii.panel : 0, borderTopRightRadius: first ? theme.radii.panel : 0, borderBottomLeftRadius: last ? theme.radii.panel : 0, borderBottomRightRadius: last ? theme.radii.panel : 0};
        // Each row draws its own slice of the section's raised card.
        return <View style={{marginHorizontal: theme.gutter, overflow: 'hidden', backgroundColor: theme.colors.raised, ...radius}}>
          <NotificationItem testID={`notification-${notice.id}`} separator={!first} notification={notice} title={message.title} body={message.body} when={defaultI18n.relativeTime(notice.createdAt)}
            subject={<Icon name={sourceIcon(notice.source)} size={theme.space.md} color={notice.severity === 'critical' ? theme.colors.record : notice.severity === 'warning' ? theme.colors.amber : theme.colors.textSecondary}/>}
            actions={actionsFor(notice, view)} menuLabel={t('next.shell.native.moreOptions')} onMenu={() => model.showMenu(notice)} style={{opacity: notice.read && view !== 'archived' ? .7 : 1}}/>
        </View>;
      }}/>;
}

const sourceIcon = (source: string): IconName => source === 'downloads' ? 'i-dl'
  : source === 'dvr' ? 'i-rec'
    : source === 'feedback' ? 'i-bubble'
      : source === 'security' ? 'i-shield'
        : source === 'scan' ? 'i-refresh'
          : source === 'networking' ? 'i-wifi'
            : source === 'storage' ? 'i-disk' : 'i-bell';
