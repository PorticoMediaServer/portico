import {NativePlaybackProblems} from './troubleshooting/PlaybackProblems';
import {NativeSupport,useNativeSupport} from './troubleshooting/Support';
import {NativeReports,useNativeReports} from './troubleshooting/Reports';
import {NativeFeedback,useNativeFeedback} from './troubleshooting/Feedback';
import {NativeMessages} from './troubleshooting/Messages';
import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Tabs} from '../../../kit/controls/Tabs';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type ViewerRole = 'owner' | 'administrator' | 'member';
type Translate = typeof defaultI18n.t;


/** Settings › Server › Troubleshooting on the phone (owner-server.html frame 33). */
export function NativeServerTroubleshooting({ports, runtime, compactHeader = false, initialTab}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; initialTab?: 'messages' | 'feedback' | 'reports' | 'support'}) {
  const theme = useTheme(), t = runtime.text.t as Translate, context = ports.context;
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const [tab, setTab] = useState(initialTab ?? 'messages');
  const feedback=useNativeFeedback(ports,runtime);
  const reports=useNativeReports(ports,runtime);
  const support=useNativeSupport(ports,runtime);
  const openReports=feedback.state.reports.filter(report=>report.status==='open'||report.status==='in-progress').length;
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.troubleshooting')}</Text>}
      <Tabs testID="server-troubleshooting-tabs" style={{marginTop: 12, marginHorizontal: -20}} value={tab} onValueChange={value => {if (value === 'messages' || value === 'feedback' || value === 'reports' || value === 'support') setTab(value);}} accessibilityLabel={t('settings.server.troubleshooting')}
        items={[{value: 'messages', label: t('web.logs.messages')}, {value: 'feedback', label: openReports ? `${t('server.feedbackTab')} ${openReports}` : t('server.feedbackTab')}, {value: 'reports', label: t('server.reportsTab')}, {value: 'support', label: t('server.support')}]}/>
      {tab === 'messages' && <><NativeMessages ports={ports} runtime={runtime}/><NativePlaybackProblems ports={ports} runtime={runtime}/></>}
      {tab === 'feedback' && feedback.group && <NativeFeedback ports={ports} runtime={runtime} group={feedback.group}/>}
      {tab === 'reports' && reports && <NativeReports group={reports}/>}
      {tab === 'support' && support && <NativeSupport group={support}/>}
    </ScrollView>
  </View>;
}
