import React, {useRef, useState} from 'react';
import type {ScrollView} from 'react-native';
import {Tabs} from '../../../kit/controls/Tabs';
import {SettingsPageFrame} from '../../../kit/frames';
import {Messages} from '../../../kit/settings/troubleshooting/Messages';
import {NativeFeedback} from '../../../kit/settings/troubleshooting/Feedback';
import {NativeReports} from '../../../kit/settings/troubleshooting/Reports';
import {NativeSkippedFiles} from '../../../kit/settings/troubleshooting/SkippedFiles';
import {NativeSupport} from '../../../kit/settings/troubleshooting/Support';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useMessages} from '../../shared/settings/useTroubleshootingMessages';
import {useNativeFeedback} from '../../shared/settings/useTroubleshootingFeedback';
import {useNativeReports} from '../../shared/settings/useTroubleshootingReports';
import {useNativeSupport} from '../../shared/settings/useTroubleshootingSupport';

type Tab = 'messages' | 'skipped' | 'feedback' | 'reports' | 'support';
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;
type Ports = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime};

function MessagesTab({ports, runtime, pageScrollY, scrollToY}: Ports & {
  pageScrollY: number; scrollToY: (y: number) => void;
}) {
  const group = useMessages(ports, runtime);
  return group ? <Messages group={group} runtime={runtime} pageScrollY={pageScrollY} scrollToY={scrollToY}/> : null;
}

export function ServerTroubleshootingPage({ports, runtime, back, server, retry, initialTab = 'messages'}: Frame & Ports & {
  retry?: React.ReactNode; initialTab?: Tab;
}) {
  const {t} = runtime.text, account = ports.context.account;
  const role = account.account.role === 'owner' ? 'owner' : account.canManage ? 'administrator' : 'member';
  const feedback = useNativeFeedback(ports, runtime), reports = useNativeReports(ports, runtime), support = useNativeSupport(ports, runtime);
  const openReports = feedback.state.reports.filter(report => report.status === 'open' || report.status === 'in-progress').length;
  const [tab, setTab] = useState(initialTab), [scrollY, setScrollY] = useState(0), scroller = useRef<ScrollView>(null);

  const items: {value: Tab; label: string}[] = [
    {value: 'messages', label: t('web.logs.messages')},
    {value: 'skipped', label: t('next.settings.troubleshootingStuckFiles')},
  ];
  if (role === 'owner') items.push(
    {value: 'feedback', label: openReports ? `${t('server.feedbackTab')} ${openReports}` : t('server.feedbackTab')},
    {value: 'reports', label: t('server.reportsTab')},
    {value: 'support', label: t('server.support')},
  );
  const active = items.some(item => item.value === tab) ? tab : 'messages';
  const title = t('settings.server.troubleshooting');

  return <SettingsPageFrame title={title} testID="server-troubleshooting" back={back}
    server={server ?? <ServerHeader serverName={ports.context.serverName} viewerRole={role}
      audience="administrators" variant="phone"/>}
    scrollRef={scroller} onScrollOffset={active === 'messages' ? setScrollY : undefined}
    tabs={<Tabs testID="server-troubleshooting-tabs" accessibilityLabel={title} value={active} items={items}
      onValueChange={value => {
        const item = items.find(choice => choice.value === value);
        if (item) {
          scroller.current?.scrollTo({y: 0, animated: false});
          setScrollY(0);
          setTab(item.value);
        }
      }}/>}>
    {retry}
    {active === 'messages' && <MessagesTab ports={ports} runtime={runtime} pageScrollY={scrollY}
      scrollToY={y => scroller.current?.scrollTo({y, animated: true})}/>}
    {active === 'skipped' && <NativeSkippedFiles ports={ports} runtime={runtime}/>}
    {active === 'feedback' && feedback.group && <NativeFeedback ports={ports} runtime={runtime} group={feedback.group}/>}
    {active === 'reports' && reports && <NativeReports group={reports}/>}
    {active === 'support' && support && <NativeSupport group={support}/>}
  </SettingsPageFrame>;
}
