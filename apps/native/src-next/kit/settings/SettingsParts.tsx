import React, {type ComponentProps, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import QRCode from 'react-native-qrcode-svg';
import type {MessageId} from '../../../../../packages/i18n/src';
import {publicTVHandoffAddress} from '../../tv/auth/TVHandoff';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Focusable} from '../controls';
import {FocusRegion} from '../focus';
import {useFrameMetrics} from '../frames/metrics';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {stateSentenceParts} from '../states/sentence-parts';
import {useTheme} from '../theme';

export function SettingsPageLink({id, testID, title, icon, selected, server, onPress, onFocus}: {
  id: string; testID?: string; title: string; icon: IconName; selected: boolean; server?: boolean; onPress: () => void; onFocus?: () => void;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone', tv = theme.isTV;
  const tint = server || selected ? theme.colors.accentStrong : theme.colors.textSecondary;
  return <Focusable focusId={id} testID={testID ?? `settings-page-${id}`} accessibilityRole="button" accessibilityLabel={title}
    accessibilityState={{selected}} onFocus={onFocus} onPress={onPress} focusKind="row" focusRadius={frame.cardRadius}
    style={{minHeight: frameSpec.settings.navigationRow[frame.form], paddingVertical: theme.space.tight, paddingHorizontal: frame.cardPadding,
      gap: theme.space.controlGap, borderRadius: frame.cardRadius, flexDirection: 'row', alignItems: 'center',
      backgroundColor: selected && !phone ? alpha(theme.colors.accent, .16) : undefined}}>
    {tv && selected && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: theme.space.xs, bottom: theme.space.xs,
      width: theme.space.hairline * 4, borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accent}}/>}
    <View style={tv ? undefined : {width: frameSpec.stateRow.tile[frame.form], height: frameSpec.stateRow.tile[frame.form], borderRadius: theme.baseRadii.control,
      backgroundColor: server ? alpha(theme.colors.accent, .16) : theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}>
      <Icon name={icon} size={theme.type.rowLabel.fontSize} color={tint}/>
    </View>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{title}</Text>
    {!tv && <Icon name="i-right" size={theme.type.rowHelp.fontSize} color={theme.colors.textTertiary}/>}
  </Focusable>;
}

export function SettingsNavigationGroup({children, server}: {children: ReactNode; server?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{marginBottom: theme.space.sm, marginHorizontal: frame.form === 'phone' ? frame.cardInset : 0,
    borderRadius: frame.cardRadius, backgroundColor: frame.form === 'phone' || server ? theme.colors.raised : undefined,
    borderWidth: server ? theme.space.hairline : 0, borderColor: alpha(theme.colors.accentStrong, .28)}}>
    {frame.form === 'phone' && !server ? React.Children.map(children, (child, index) => <View>
      {index > 0 && <View pointerEvents="none" style={{position: 'absolute', left: frame.cardPadding * 4, right: 0, top: 0,
        height: theme.space.hairline, backgroundColor: theme.colors.line}}/>}
      {child}
    </View>) : children}
  </View>;
}

export function SettingsControlRows(props: Omit<ComponentProps<typeof FocusRegion>, 'style'>) {
  const frame = useFrameMetrics();
  return <FocusRegion {...props} style={{gap: frame.sectionGap}}/>;
}

export function SettingsHelp({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text testID={testID} style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginHorizontal: frame.cardInset}]}>{children}</Text>;
}

export function SettingsSearchGroup({label, children}: {label: string; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{gap: frame.headingGap}}>
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{label}</Text>
    {children}
  </View>;
}

export function SettingsNotificationCount({count}: {count: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.micro, {color: theme.colors.ink}]}>{count}</Text>;
}

export function SettingsKeyHints({t}: {t: NativeShellRuntime['text']['t']}) {
  const theme = useTheme();
  const key = (label: string) => <View style={{minWidth: theme.targets.touch, padding: theme.space.tight,
    borderRadius: theme.baseRadii.control, borderWidth: theme.space.hairline,
    borderColor: theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{label}</Text>
  </View>;
  const words = (label: string) => <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{label}</Text>;
  return <View testID="settings-key-hints" style={{gap: theme.space.xs, marginTop: theme.space.lg}}>
    <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.controlGap}}>
      {key(t('next.controls.native.up'))}{key(t('next.controls.native.down'))}{words(t('next.shell.settingsHintChoose'))}
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.controlGap}}>
      {key(t('next.controls.native.right'))}{words(t('next.shell.settingsHintEdit'))}{key(t('next.controls.native.left'))}{words(t('next.shell.settingsHintPages'))}
    </View>
  </View>;
}

export function SettingsManagementCard({url, text, server}: {url: string; text: NativeShellRuntime['text']; server?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), address = publicTVHandoffAddress(url), t = text.t;
  const qr = frameSpec.settings.managementQR;
  const title = t(server ? 'tv.server.handoffTitle' : 'account.tvHandoffTitle');
  const intro = t(server ? 'tv.server.handoffBody' : 'settings.openOnWeb', {url: '{url}'});
  return <View testID="settings-management-handoff" style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.lg,
    padding: frame.cardPadding, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised,
    borderWidth: theme.space.hairline, borderColor: theme.colors.line}}>
    <View style={{padding: theme.space.sm, borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.white}}>
      <QRCode value={address.url} size={qr}/>
    </View>
    <View style={{flex: 1, gap: theme.space.controlGap}}>
      <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{stateSentenceParts(intro, {url: address.display}, ['url']).map((part, index) =>
        <Text key={index} style={part.strong ? [theme.type.captionStrong, {color: theme.colors.text}] : undefined}>{part.text}</Text>)}</Text>
    </View>
  </View>;
}

export function PhoneDashboardCard({t, update, onPress}: {t: (key: MessageId) => string; update: boolean; onPress: () => void}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable testID="settings-dashboard-card" accessibilityRole="button" onPress={onPress}
    accessibilityLabel={[t('settings.server.dashboard'), t('next.shell.dashboardSummary'), ...(update ? [t('next.dashboard.update')] : [])].join(', ')}
    focusKind="row" focusRadius={frame.cardRadius}
    style={{marginHorizontal: frame.cardInset, marginTop: theme.space.xs, minHeight: frame.rowTwoLine, padding: frame.cardPadding,
      gap: theme.space.controlGap, borderRadius: frame.cardRadius, flexDirection: 'row', alignItems: 'center',
      backgroundColor: theme.colors.raised, borderWidth: theme.space.hairline, borderColor: alpha(theme.colors.accentStrong, .28)}}>
    <View style={{width: frameSpec.stateRow.tile[frame.form], height: frameSpec.stateRow.tile[frame.form], borderRadius: theme.baseRadii.control, backgroundColor: alpha(theme.colors.accent, .16),
      alignItems: 'center', justifyContent: 'center'}}><Icon name="i-pulse" size={theme.type.rowLabel.fontSize} color={theme.colors.accentStrong}/></View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{t('next.shell.server')}</Text>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{t('settings.server.dashboard')}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('next.shell.dashboardSummary')}</Text>
    </View>
    {update && <Text testID="settings-dashboard-update" style={[theme.type.captionStrong, {color: theme.colors.amber}]}>{t('next.dashboard.update')}</Text>}
    <Icon name="i-right" size={theme.type.rowLabel.fontSize} color={theme.colors.textTertiary}/>
  </Focusable>;
}
