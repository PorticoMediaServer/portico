import React, {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {CountdownRing} from '../../kit/states';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../tv/auth/entry-revision';
import type {DirectoryRow} from './directory-chooser';
import {PhoneOfflineServers} from './PhoneOfflineServers';

type Props = {rows: readonly DirectoryRow[]; scope: object | undefined; revision: number; isCurrent: () => boolean;
  onDownloads?: () => void; username?: string; onSettings?: () => void; onJoin?: () => void; onFind?: () => void;
  busy: boolean; retryIn?: number; retryProgress?: number; activeId?: string; onRetry: (id: string) => void; onBack: () => void; onRefresh: () => void};
type Ticket = {live: boolean; scope: object};
/** connect37's same-day form: the cached time is drawn only when it falls on the
 * viewer's own local day, in the profile's zone and clock; other days keep the
 * directory's existing last-seen wording. */
function lastSeenText(row: DirectoryRow, now: number): string | undefined {
  if (!row.subtitle) return undefined;
  const at = row.lastSeen?.at, parsed = at ? Date.parse(at) : NaN;
  if (at && Number.isFinite(parsed)) {
    const day = (value: number) => defaultI18n.formatDate(value, {year: 'numeric', month: '2-digit', day: '2-digit'});
    if (day(parsed) === day(now)) return defaultI18n.t('web.chooser.lastSeenToday', {time: defaultI18n.time(at)});
  }
  return defaultI18n.t(row.subtitle.key, row.subtitle.values);
}
/** Actual directory observations only. The parent owns one serialized connection
 * and its existing captured-issuer retry timer; this view never starts a session. */
export function TVOfflineServers(props: Props) {
  const theme = useTheme();
  const scope = useMemo(() => ({owner: props.scope, revision: props.revision}), [props.scope, props.revision]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {scope, live: true}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && props.isCurrent();
  if (!theme.isTV) {
    const action = (command?: () => void) => () => {if (current() && !props.busy) command?.();};
    return <PhoneOfflineServers rows={props.rows} busy={props.busy} activeId={props.activeId} retryIn={props.retryIn} username={props.username}
      onRetry={id => {if (current() && !props.busy && props.rows.some(row => row.id === id)) props.onRetry(id);}}
      onDownloads={props.onDownloads ? action(props.onDownloads) : undefined}
      onSettings={props.onSettings ? action(props.onSettings) : undefined} onJoin={props.onJoin ? action(props.onJoin) : undefined} onFind={props.onFind ? action(props.onFind) : undefined} onRefresh={action(props.onRefresh)} onBack={action(props.onBack)}/>;
  }
  return <FocusProvider><TVOfflineList rows={props.rows} busy={props.busy} activeId={props.activeId} retryIn={props.retryIn} retryProgress={props.retryProgress}
    onRetry={props.onRetry} onRefresh={props.onRefresh} current={current}/></FocusProvider>;
}

/** connect37 at ten feet: every server with the cached last-seen fact the directory
 * actually published, its Owner mark and its own Try again, and the first row's
 * action as the drawn entry. A retry — that row's own, the automatic one, or
 * Refresh — disables and detaches every control, so the list asks for that entry
 * again when the pending run ends. */
function TVOfflineList({rows, busy, activeId, retryIn, retryProgress, current, onRetry, onRefresh}: {
  rows: readonly DirectoryRow[]; busy: boolean; activeId?: string; retryIn?: number; retryProgress?: number; current: () => boolean;
  onRetry: (id: string) => void; onRefresh: () => void;
}) {
  const t = defaultI18n.t, theme = useTheme(), first = rows[0], now = Date.now();
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision},
    first ? {regionId: 'accounts.offline.rows', childId: `retry:${first.id}`} : undefined);
  return <View testID="tv-offline-servers" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="side" fill/>
    <ScrollView contentContainerStyle={{paddingHorizontal: theme.layout.gutter.tvAuth, paddingTop: 80, paddingBottom: 80, gap: 24}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('accounts.session.portico')}</Text>
      <Text accessibilityRole="header" style={[nativeType({size: 44, lineHeight: 52, weight: 700, tracking: -.03}, theme.textScale), {color: theme.colors.text}]}>{t('accounts.tvOffline.title')}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('accounts.tvOffline.body')}</Text>
      <FocusRegion id="accounts.offline.rows" entry={first ? `retry:${first.id}` : undefined} enabled={!busy} style={{gap: 14}}>
        {rows.map((row, index) => {
          const seen = lastSeenText(row, now);
          return <View key={row.id} style={{height: 112, paddingLeft: 26, paddingRight: 32, borderRadius: 22, flexDirection: 'row', alignItems: 'center', gap: 24,
          backgroundColor: 'rgba(16, 33, 45, 0.74)', borderWidth: 1.5, borderColor: 'rgba(237, 245, 250, 0.09)', opacity: row.offline ? 0.6 : 1}}>
          <View style={{width: 68, height: 68, borderRadius: 18, alignItems: 'center', justifyContent: 'center', flexShrink: 0, backgroundColor: theme.colors.raisedBright}}>
            <Icon name="i-server" size={34} color={theme.colors.textTertiary}/>
          </View>
          <View style={{flex: 1, gap: 4}}>
            <Text style={[nativeType({size: 32, weight: 700, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{row.name}</Text>
            {seen && <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{seen}</Text>}
          </View>
          {row.owner && <Text style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale),
            {color: theme.colors.textSecondary, backgroundColor: 'rgba(7, 16, 24, 0.6)', paddingHorizontal: 12, paddingVertical: 4, borderRadius: 8}]}>{t('accounts.settings.owner')}</Text>}
          <Button focusId={`retry:${row.id}`} size="compact" icon="i-retry" iconSize={28} hasTVPreferredFocus={index === 0} disabled={busy} loading={busy && activeId === row.id || undefined}
            style={{height: 64, paddingHorizontal: 28}} textStyle={{fontSize: 24 * theme.textScale}}
            onPress={() => {if (current() && !busy) onRetry(row.id);}}>{t('web.chooser.tryAgain')}</Button>
        </View>;})}
      </FocusRegion>
      {retryIn !== undefined && !busy && <View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginTop: 18}}>
        <CountdownRing progress={retryProgress}/>
        <Text style={[nativeType({size: 24, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('web.chooser.checkingAgain', {seconds: retryIn})}</Text>
      </View>}
      <Text style={[nativeType({size: 22, lineHeight: 33, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 6, maxWidth: 1100}]}>{t('accounts.tvOffline.help')}</Text>
      <View style={{flexDirection: 'row', gap: 24}}>
        <Button variant="text" disabled={busy} onPress={() => {if (current()) onRefresh();}}>{t('accounts.servers.refresh')}</Button>
      </View>
    </ScrollView>
  </View>;
}
