import React from 'react';
import {Text, View} from 'react-native';
import Svg, {Circle} from 'react-native-svg';
import {Button, Focusable} from '../../../kit/controls';
import {Icon, type IconName} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import type {OwnerAlertView, OwnerJobView, OwnerStatistic, Translate} from './model';

/** One drawn section: title, optional count beside it, optional right control, then its body. */
export function OwnerSection({title, sub, right, children, first = false}: {title: string; sub?: string; right?: React.ReactNode; children: React.ReactNode; first?: boolean}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? 20 : 32, paddingHorizontal: 16}}>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, paddingHorizontal: 4, marginBottom: 12}}>
      <Text accessibilityRole="header" style={[shellType(20, 700, 'normal', -.015), {color: theme.colors.text}]}>{title}</Text>
      {sub ? <Text numberOfLines={1} style={[shellType(13, 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>{sub}</Text> : null}
      {right ? <View style={{marginLeft: 'auto'}}>{right}</View> : null}
    </View>
    {children}
  </View>;
}

/** A quiet section keeps its place in one line, as the frame draws it. */
export function OwnerCalm({icon, title, body, tone = 'quiet', action}: {icon: IconName; title: string; body?: string; tone?: 'quiet' | 'good' | 'error'; action?: {label: string; onPress: () => void}}) {
  const theme = useTheme();
  const lead = tone === 'good' ? theme.colors.healthy : tone === 'error' ? theme.colors.danger : theme.colors.textTertiary;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, padding: 16, borderRadius: 14, backgroundColor: theme.colors.raised}}>
    <View style={{width: 38, height: 38, borderRadius: 19, alignItems: 'center', justifyContent: 'center', backgroundColor: tone === 'quiet' ? alpha(theme.colors.text, .07) : alpha(lead, .12)}}><Icon name={icon} size={18} color={lead}/></View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[shellType(15, 700, 19.5), {color: theme.colors.text}]}>{title}</Text>
      {body ? <Text style={[shellType(13.5, 400, 19.6), {color: theme.colors.textTertiary, marginTop: 3}]}>{body}</Text> : null}
      {action ? <View style={{marginTop: 10, alignSelf: 'flex-start'}}><Button variant="glass" size="xs" onPress={action.onPress}>{action.label}</Button></View> : null}
    </View>
  </View>;
}

/** An alert: its severity, name, catalogued explanation and the one action the server allows. */
export function OwnerAlertCard({alert, t, onAcknowledge, onFixPermissions}: {alert: OwnerAlertView; t: Translate; onAcknowledge: () => void; onFixPermissions?: () => void}) {
  const theme = useTheme();
  const tone = alert.tone === 'danger' ? theme.colors.danger : alert.tone === 'warning' ? theme.colors.amber : theme.colors.textSecondary;
  return <View testID={`owner-alert-${alert.id}`} style={{paddingHorizontal: 16, paddingTop: 16, paddingBottom: 14, borderRadius: 14, backgroundColor: theme.colors.raised, opacity: alert.acknowledged ? .66 : 1}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 7, marginBottom: 6}}>
      <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: tone}}/>
      <Text style={[shellType(12.5, 700), {color: tone}]}>{alert.severity}</Text>
    </View>
    <Text style={[shellType(15.5, 700, 20.2), {color: theme.colors.text}]}>{alert.title}</Text>
    {alert.body ? <Text style={[shellType(14, 400, 20.3), {color: theme.colors.textSecondary, marginTop: 5}]}>{alert.body}</Text> : null}
    <Text style={[shellType(12.5, 600), {color: theme.colors.textTertiary, marginTop: 8}]}>{alert.meta}</Text>
    {!alert.acknowledged ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: 14}}>
      {alert.permissions && onFixPermissions ? <Button variant="primary" size="xs" onPress={onFixPermissions}>{t('web.maintenance.fixPermissions')}</Button> : null}
      <Button variant="text" size="xs" onPress={onAcknowledge}>{t('web.overview.acknowledge')}</Button>
    </View> : null}
  </View>;
}

/** The task ring: a real fraction when the server publishes a total, otherwise a state. */
function OwnerRing({progress, icon, tone, waiting = false, size = 44}: {progress?: number; icon: IconName; tone: OwnerJobView['tone']; waiting?: boolean; size?: number}) {
  const theme = useTheme();
  const stroke = 3, radius = (size - stroke) / 2, circumference = 2 * Math.PI * radius;
  const color = tone === 'healthy' ? theme.colors.healthy : tone === 'danger' ? theme.colors.danger : tone === 'warning' ? theme.colors.textTertiary : tone === 'accent' ? theme.colors.accentStrong : theme.colors.textSecondary;
  return <View style={{width: size, height: size, alignItems: 'center', justifyContent: 'center'}}>
    <Svg accessible={false} width={size} height={size} style={{position: 'absolute', transform: [{rotate: '-90deg'}]}}>
      <Circle cx={size / 2} cy={size / 2} r={radius} stroke={waiting ? alpha(theme.colors.text, .2) : tone === 'healthy' ? alpha(theme.colors.healthy, .4) : tone === 'danger' ? alpha(theme.colors.danger, .45) : alpha(theme.colors.text, .13)} strokeDasharray={waiting ? '3 5' : undefined} strokeWidth={stroke} fill="none"/>
      {progress !== undefined ? <Circle cx={size / 2} cy={size / 2} r={radius} stroke={color} strokeWidth={stroke} strokeLinecap="round" strokeDasharray={`${Math.max(0, Math.min(1, progress)) * circumference} ${circumference}`} fill="none"/> : null}
    </Svg>
    <Icon name={icon} size={19} color={color}/>
  </View>;
}

/** A task row: ring, name, what it is doing, its real progress, and its menu when the server offers one. */
export function OwnerJobRow({job, onMenu}: {job: OwnerJobView; onMenu?: () => void}) {
  const theme = useTheme();
  const content = <>
    <OwnerRing progress={job.progress} icon={job.icon} tone={job.tone} waiting={job.progress === undefined && job.tone === 'neutral'}/>
    <View style={{flex: 1, minWidth: 0}}>
      <Text numberOfLines={1} style={[shellType(15, 700), {color: theme.colors.text}]}>{job.title}</Text>
      {job.subtitle ? <Text numberOfLines={1} style={[shellType(13, 400, 18.2), {color: theme.colors.textTertiary, marginTop: 2}]}>{job.subtitle}</Text> : null}
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}>
      <Text style={[shellType(job.progress === undefined ? 13 : 15, 700), {color: job.tone === 'danger' ? theme.colors.danger : job.progress === undefined ? theme.colors.textTertiary : theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{job.status}</Text>
      {onMenu ? <Icon name="i-more" size={18} color={theme.colors.textTertiary}/> : null}
    </View>
  </>;
  return onMenu
    ? <Focusable testID={`owner-job-${job.id}`} accessibilityRole="button" accessibilityLabel={job.title} onPress={onMenu} focusKind="row" focusRadius={14} style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 14, paddingVertical: 13}}>{content}</Focusable>
    : <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 14, paddingVertical: 13}}>{content}</View>;
}

/** A group's own label inside Background activity: library scans first, then the other tasks. */
export function OwnerSubheading({children, first = false}: {children: React.ReactNode; first?: boolean}) {
  const theme = useTheme();
  return <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginHorizontal: 4, marginTop: first ? 0 : 18, marginBottom: 8}]}>{children}</Text>;
}

/** Tasks of one group, with the hairline between them drawn from inside the surface. */
export function OwnerJobGroup({children}: {children: React.ReactNode}) {
  const theme = useTheme(), rows = React.Children.toArray(children).filter(Boolean);
  return <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>
    {rows.map((row, index) => <View key={index}>{row}{index > 0 && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 72, right: 0, height: 1, backgroundColor: theme.colors.lineSoft}}/>}</View>)}
  </View>;
}

/** The four viewing figures, with the Converted share under its own count. */
export function OwnerStatisticTiles({figures}: {figures: readonly OwnerStatistic[]}) {
  const theme = useTheme();
  if (!figures.length) return null;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 2, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.lineSoft}}>
    {figures.map(figure => <View key={figure.id} style={{width: '49.6%', flexGrow: 1, backgroundColor: theme.colors.raised, paddingHorizontal: 16, paddingTop: 14, paddingBottom: 15}}>
      <Text style={[shellType(13, 600), {color: theme.colors.textTertiary}]}>{figure.label}</Text>
      <Text style={[shellType(34, 800, 'normal', -.03), {color: theme.colors.text, marginTop: 3}]}>{figure.value}</Text>
      {figure.note ? <Text style={[shellType(12.5, 600), {color: theme.colors.textTertiary, marginTop: 2}]}>{figure.note}</Text> : null}
    </View>)}
  </View>;
}
