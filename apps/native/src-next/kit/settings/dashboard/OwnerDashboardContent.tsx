import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, useWindowDimensions, View, type RefreshControlProps} from 'react-native';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import {ServerHeader} from '../../shell';
import {useTheme} from '../../theme';
import type {OwnerAlertView, OwnerHealthWindow, OwnerJobView, OwnerSessionTotals, OwnerStreamView, OwnerViewing, OwnerViewingPeriod, Translate} from '../../../screens/settings-live/owner-dashboard/model';
import {ownerCompactWindow, ownerStreamTotals, ownerUsesCompactRows} from '../../../screens/settings-live/owner-dashboard/model';
import {OwnerAlertCard, OwnerCalm, OwnerJobGroup, OwnerJobRow, OwnerSection, OwnerSubheading} from './parts';
import {OwnerHealthSection} from './health';
import {OwnerStatisticsSection} from './statistics';
import {OwnerUpdateCard, type OwnerUpdateView} from './update-card';
import {OwnerCompactRows, OwnerNowPlayingHeader, OwnerShowAll, OwnerStreamCards, OwnerStreamTotals, type OwnerStreamEntry} from './streams';
import {BarSurface, TopBar} from '../../layout/TopBar';

export type OwnerDashboardPageProps = Readonly<{
  t: Translate;
  role: 'owner' | 'administrator';
  serverName: string;
  topInset: number;
  health: Readonly<{degraded: boolean; status: string; line: string; uptimeSeconds?: number}>;
  updated?: string;
  loading: boolean;
  error?: unknown;
  streams: readonly OwnerStreamEntry[];
  /** The server's own totals over every stream; never a page count standing in for them. */
  streamTotals: OwnerSessionTotals;
  expanded: boolean;
  allStreams: readonly OwnerStreamEntry[];
  allError?: unknown;
  people?: string;
  alerts: readonly OwnerAlertView[];
  /** owner-dashboard 2b: the update's own card at the end of Alerts, only while the owner's release check reports one. */
  update?: OwnerUpdateView;
  onReleaseNotes?: () => void;
  onCheckUpdate?: () => void;
  jobs: readonly OwnerJobView[];
  telemetry?: TelemetryReading;
  telemetryError?: unknown;
  healthWindow: OwnerHealthWindow;
  healthNote?: string;
  summary?: OwnerViewing;
  statisticsError?: unknown;
  viewingPeriod: OwnerViewingPeriod;
  onBack: () => void;
  /** owner-dashboard 2: pulling the page down refreshes it with the system ring (web keeps a Refresh button instead). */
  refreshControl?: React.ReactElement<RefreshControlProps>;
  onRetry: () => void;
  onHealthWindow: (window: OwnerHealthWindow) => void;
  onViewingPeriod: (period: OwnerViewingPeriod) => void;
  onFixPermissions?: (alert: OwnerAlertView) => void;
  onJobMenu: (job: OwnerJobView) => void;
  onStop: (stream: OwnerStreamView) => void;
  onStreamMenu: (stream: OwnerStreamView) => void;
  onShowAll: () => void;
  onShowFewer: () => void;
  onRetryAll: () => void;
  /** The same page inside the tablet Settings split: no page-level Back header. */
  inline?: boolean;
}>;

/** One page for every width of the owner Dashboard's phone layout (owner-dashboard.html frames 2–8). */
export function OwnerDashboardContent(props: OwnerDashboardPageProps) {
  const {t, role} = props, theme = useTheme(), viewport = useWindowDimensions().height;
  const [scrolled, setScrolled] = useState(false), [stuck, setStuck] = useState(false);
  const [first, setWindow] = useState<{first: number; last: number}>({first: 0, last: 0});
  const bar = props.inline ? 0 : props.topInset + 48, headerTop = useRef(0), listTop = useRef(0), scrollTop = useRef(0);
  const owner = role === 'owner', compact = ownerUsesCompactRows(props.streamTotals), expanded = props.expanded && compact;
  const rows = expanded ? props.allStreams : props.streams;
  const count = props.streamTotals.streams, collapsedLast = Math.min(rows.length, 10) - 1;
  const totals = ownerStreamTotals(props.streamTotals, t);
  useEffect(() => {
    if (!expanded) {
      setWindow(previous => previous.first === 0 && previous.last === collapsedLast ? previous : {first: 0, last: collapsedLast});
      setStuck(previous => previous ? false : previous);
      return;
    }
    const next = ownerCompactWindow(scrollTop.current - listTop.current, viewport - bar, rows.length);
    setWindow(previous => previous.first === next.first && previous.last === next.last ? previous : next);
    const sticks = scrollTop.current + bar >= headerTop.current;
    setStuck(previous => previous === sticks ? previous : sticks);
  }, [expanded, collapsedLast, rows.length, viewport, bar]);
  const onScroll = (event: {nativeEvent: {contentOffset: {y: number}}}) => {
    const y = event.nativeEvent.contentOffset.y;
    scrollTop.current = y;
    setScrolled(y > 56);
    if (!expanded) return;
    const next = ownerCompactWindow(y - listTop.current, viewport - bar, rows.length);
    setWindow(previous => previous.first === next.first && previous.last === next.last ? previous : next);
    const sticks = y + bar >= headerTop.current;
    setStuck(previous => previous === sticks ? previous : sticks);
  };
  return <View style={{flex: 1}}>
    <ScrollView testID="owner-dashboard-scroll" refreshControl={props.refreshControl} scrollEventThrottle={16} onScroll={onScroll} contentContainerStyle={{paddingTop: bar + theme.space.xs, paddingBottom: theme.space.pageBottom, rowGap: theme.space.xs}}>
      <View style={{marginHorizontal: theme.space.sm}}><ServerHeader serverName={props.serverName} viewerRole={role} audience="administrators" variant="page"
        facts={[...(props.health.status ? [{id: 'health', text: props.health.status, tone: props.health.degraded ? 'warning' as const : 'normal' as const}] : []),
          ...[props.health.line, props.updated].filter((fact): fact is string => !!fact).map((text, index) => ({id: String(index), text}))]}/></View>
      {props.error !== undefined && props.streams.length ? <View style={{paddingHorizontal: 16}}><OwnerCalm icon="i-warn" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: props.onRetry}}/></View> : null}
      <OwnerNowPlayingHeader t={t} people={props.people} count={count} expanded={expanded} onShowFewer={props.onShowFewer} hidden={stuck}
        onLayout={event => {headerTop.current = event.nativeEvent.layout.y;}}/>
      {compact ? <>
        <OwnerStreamTotals totals={totals}/>
        {!rows.length && props.error !== undefined ? <View style={{marginHorizontal: 16}}><OwnerCalm icon="i-warn" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: props.onRetry}}/></View> : null}
        {!rows.length && props.error === undefined && props.loading ? <OwnerCalm icon="i-pulse" title={t('next.states.loading')}/> : null}
        {rows.length ? <View onLayout={event => {listTop.current = event.nativeEvent.layout.y;}}>
          <OwnerCompactRows streams={rows} window={expanded ? first : {first: 0, last: collapsedLast}} onMenu={props.onStreamMenu} t={t}/>
        </View> : null}
        {expanded && props.allError !== undefined ? <View style={{marginHorizontal: 16, marginTop: 12}}><OwnerCalm icon="i-warn" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: props.onRetryAll}}/></View> : null}
        <OwnerShowAll expanded={expanded} count={count} onPress={expanded ? props.onShowFewer : props.onShowAll} t={t}/>
      </> : <OwnerStreamCards streams={props.streams} loading={props.loading} error={props.error} onRetry={props.onRetry} onStop={props.onStop} onMenu={props.onStreamMenu} t={t}/>}
      {owner ? <>
        <OwnerSection title={t('next.dashboard.alerts')} sub={props.alerts.length ? String(props.alerts.length) : undefined}>
          {props.alerts.length ? props.alerts.map((alert, index) => <View key={alert.id} style={{marginTop: index ? 12 : 0}}><OwnerAlertCard alert={alert} t={t} {...(props.onFixPermissions ? {onFixPermissions: () => props.onFixPermissions!(alert)} : {})}/></View>)
            : <OwnerCalm icon="i-check" tone="good" title={t('web.overview.noAlerts')}/>}
          {props.update ? <View style={{marginTop: 12}}><OwnerUpdateCard update={props.update} t={t} onReleaseNotes={() => props.onReleaseNotes?.()} onCheck={() => props.onCheckUpdate?.()}/></View> : null}
          {props.update?.checkFailed ? <View style={{marginTop: 12}}><OwnerCalm icon="i-warn" tone="error" title={t('next.dashboard.updateCheckFailed')} body={t('next.dashboard.updateCheckFailedBody')} action={{label: t('next.shell.tryAgain'), onPress: () => props.onCheckUpdate?.()}}/></View> : null}
        </OwnerSection>
        <OwnerSection title={t('settings.server.activity')}>
          {props.jobs.length ? <>
            {props.jobs.some(job => job.scan) ? <><OwnerSubheading first>{t('next.shell.libraryScans')}</OwnerSubheading><OwnerJobGroup>{props.jobs.filter(job => job.scan).map(job => <OwnerJobRow key={job.id} job={job} onMenu={() => props.onJobMenu(job)}/>)}</OwnerJobGroup></> : null}
            {props.jobs.some(job => !job.scan) ? <><OwnerSubheading first={!props.jobs.some(job => job.scan)}>{t('next.dashboard.tasks')}</OwnerSubheading><OwnerJobGroup>{props.jobs.filter(job => !job.scan).map(job => <OwnerJobRow key={job.id} job={job} onMenu={() => props.onJobMenu(job)}/>)}</OwnerJobGroup></> : null}
          </> : <OwnerCalm icon="i-clock" title={t('web.activity.noTasks')} body={t('web.activity.noTasksBody')}/>}
        </OwnerSection>
        <OwnerHealthSection reading={props.telemetry} error={props.telemetryError} window={props.healthWindow} onWindow={props.onHealthWindow} note={props.healthNote} onRetry={props.onRetry} t={t}/>
        <OwnerStatisticsSection summary={props.summary} error={props.statisticsError} period={props.viewingPeriod} onPeriod={props.onViewingPeriod} onRetry={props.onRetry} t={t}/>
      </> : null}
    </ScrollView>
    {expanded && stuck ? <View style={{position: 'absolute', top: bar, left: 0, right: 0, zIndex: 10}}>
      <BarSurface/>
      <OwnerNowPlayingHeader t={t} people={props.people} count={count} expanded onShowFewer={props.onShowFewer} sticky/>
    </View> : null}
    {!props.inline && <View style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 20}}>
      <TopBar
        title={t('next.dashboard.title')}
        back={{label: t('action.back'), onPress: props.onBack, testID: 'owner-dashboard-back'}}
        surface={scrolled}/>
    </View>}
  </View>;
}
