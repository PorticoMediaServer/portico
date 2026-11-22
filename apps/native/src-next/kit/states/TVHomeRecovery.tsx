import React, {type RefObject} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {SidePanel} from '../overlays';
import {useTheme} from '../theme';
import {StateLayout} from './StateLayout';

/** states.html 5 on TV: Home's server failure inside the shell, with its countdown, Try now, Switch server, Details and the
 * connection help; Details opens a side panel of the attempt and address facts. */
export function TVHomeRecovery({title, body, retry, facts, detailsOpen, opener, onRetryNow, onSwitchServer, onDetails, onCloseDetails}: {
  title: string; body?: string; retry: Readonly<{progress: number | null; label: string; detail?: string}>; facts: readonly string[];
  detailsOpen: boolean; opener: RefObject<View | null>; onRetryNow: () => void; onSwitchServer?: () => void; onDetails: () => void;
  onCloseDetails: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <>
    <StateLayout icon="i-srvoff" tone="warning" testID="tv-home-recovery" title={title} body={body} retry={retry}
      primaryAction={{id: 'retry', label: t('next.states.tryNow'), icon: 'i-retry', preferredFocus: true, onPress: onRetryNow}}
      secondaryAction={onSwitchServer ? {id: 'switch-server', label: t('next.states.switchServer'), icon: 'i-server', onPress: onSwitchServer} : undefined}
      detailsAction={{id: 'details', label: t('next.states.details'), ref: opener, accessibilityState: {expanded: detailsOpen}, onPress: onDetails}}
      help={t('next.states.native.tvConnectionHelp')}/>
    <SidePanel testID="tv-home-recovery-details" open={detailsOpen} onOpenChange={open => {if (!open) onCloseDetails();}}
      title={t('next.states.details')} topInset={0} width={700} returnFocusRef={opener}>
      <View style={{gap: theme.space.metaGap}}>
        {facts.map(line => <Text key={line} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{line}</Text>)}
      </View>
    </SidePanel>
  </>;
}
