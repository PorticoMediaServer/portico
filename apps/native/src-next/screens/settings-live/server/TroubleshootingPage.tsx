import {NativeSupport,useNativeSupport} from './troubleshooting/Support';
import {NativeReports,useNativeReports} from './troubleshooting/Reports';
import {NativeFeedback,useNativeFeedback} from './troubleshooting/Feedback';
import {NativeMessages} from './troubleshooting/Messages';
import {NativeSkippedFiles} from './troubleshooting/SkippedFiles';
import React, {useRef, useState} from 'react';
import {ScrollView, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Tabs} from '../../../kit/controls/Tabs';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {SettingsPageFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';

type ViewerRole = 'owner' | 'administrator' | 'member';
type Translate = typeof defaultI18n.t;


/** Settings › Server › Troubleshooting on the phone (owner-server.html frame 33). An administrator reads Messages and the
 *  files Portico could not prepare (console rule 1); the other parts are the owner's. */
/** On the Settings page frame (header, Back, server strip, tabs, gutters, scroll). Messages follow the page's scroll from
 * the top of this page's own body: the frame's scroll also holds the tabs and its column, so the body measures where it
 * starts in that scroll and hands Messages the scroll from there (and adds it back when Messages scrolls the page).
 * `notice` is the screen's own error, shown first. */
export function NativeServerTroubleshooting({ports, runtime, back, notice, initialTab}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; back?: Readonly<{label: string; onPress: () => void}>; notice?: React.ReactNode; initialTab?: 'messages' | 'feedback' | 'reports' | 'support' | 'skipped'}) {
  const frame = useFrameMetrics(), t = runtime.text.t as Translate, context = ports.context;
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const [tab, setTab] = useState(initialTab ?? 'messages'), [scrollY, setScrollY] = useState(0), scroller = useRef<ScrollView>(null);
  const body = useRef<View>(null), scrolled = useRef(0), [bodyY, setBodyY] = useState(0);
  // Where the body starts in the scroll's content: its place on screen less the scroll's, plus how far it is scrolled.
  const measureBody = () => body.current?.measureInWindow((_x, y) => (scroller.current as unknown as View | null)?.measureInWindow((_left: number, top: number) => setBodyY(Math.max(0, y - top + scrolled.current))));
  const feedback=useNativeFeedback(ports,runtime);
  const reports=useNativeReports(ports,runtime);
  const support=useNativeSupport(ports,runtime);
  const openReports=feedback.state.reports.filter(report=>report.status==='open'||report.status==='in-progress').length;
  return <SettingsPageFrame title={t('settings.server.troubleshooting')} back={back} scrollRef={scroller}
    onScroll={event => {scrolled.current = event.nativeEvent.contentOffset.y; setScrollY(event.nativeEvent.contentOffset.y);}}
    server={<ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant={frame.form === 'phone' ? 'phone' : 'page'}/>}
    tabs={<Tabs testID="server-troubleshooting-tabs" value={tab} onValueChange={value => {if (value === 'messages' || value === 'feedback' || value === 'reports' || value === 'support' || value === 'skipped') setTab(value);}} accessibilityLabel={t('settings.server.troubleshooting')}
        items={viewerRole !== 'owner' ? [{value: 'messages', label: t('web.logs.messages')}, {value: 'skipped', label: t('next.settings.troubleshootingStuckFiles')}] : [{value: 'messages', label: t('web.logs.messages')}, {value: 'skipped', label: t('next.settings.troubleshootingStuckFiles')}, {value: 'feedback', label: openReports ? `${t('server.feedbackTab')} ${openReports}` : t('server.feedbackTab')}, {value: 'reports', label: t('server.reportsTab')}, {value: 'support', label: t('server.support')}]}/>}>
    {notice}<View ref={body} onLayout={measureBody}>
      {tab === 'skipped' && <NativeSkippedFiles ports={ports} runtime={runtime}/>}
      {tab === 'messages' && <><NativeMessages ports={ports} runtime={runtime} pageScrollY={scrollY - bodyY} scrollToY={y => scroller.current?.scrollTo({y: y + bodyY, animated: true})}/></>}
      {viewerRole === 'owner' && tab === 'feedback' && feedback.group && <NativeFeedback ports={ports} runtime={runtime} group={feedback.group}/>}
      {viewerRole === 'owner' && tab === 'reports' && reports && <NativeReports group={reports}/>}
      {viewerRole === 'owner' && tab === 'support' && support && <NativeSupport group={support}/>}
    </View>
  </SettingsPageFrame>;
}
