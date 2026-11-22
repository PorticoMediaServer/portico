import React, {type ReactNode, type Ref} from 'react';
import {Text, View} from 'react-native';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

type Tone = 'healthy' | 'warning' | 'danger' | string;

/** One Live TV source (live-owner 1): its name, how many channels and programmes, its tuners, when its guide was updated,
 * a dot and word for how it's doing, and its ⋯; files it skipped say so in amber underneath. Pressing it opens its settings. */
export function LiveSourceCard({name, facts, tuners, updated, status, skipped, onPress, menuLabel, onMenu, menuRef, testID}: {name: string; facts: readonly string[];
  tuners?: string; updated?: string; status: Readonly<{text: string; tone: Tone}>; skipped?: string; onPress: () => void; menuLabel: string; onMenu: () => void;
  menuRef: Ref<View>; testID: string}) {
  const theme = useTheme();
  const tone = theme.colors[status.tone === 'healthy' ? 'healthy' : status.tone === 'warning' ? 'amber' : status.tone === 'danger' ? 'record' : 'textTertiary'];
  const line = (text: string, quiet = false) => <Text style={[theme.type.rowHelp, {color: quiet ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{text}</Text>;
  return <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, padding: theme.space.sm - 2, gap: theme.space.xs}}>
    <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.sm - 4}}>
      <Focusable testID={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={name} onPress={onPress} style={{flex: 1, gap: 4}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{name}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap, rowGap: 3}}>{facts.map((part, index) => <React.Fragment key={index}>{line(part, true)}</React.Fragment>)}</View>
        {tuners !== undefined && line(tuners)}
        {updated !== undefined && line(updated, true)}
        <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: 2}}>
          <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: tone}}/>
          <Text style={[theme.type.captionStrong, {color: tone}]}>{status.text}</Text>
        </View>
      </Focusable>
      <Focusable ref={menuRef} focusKind="button" accessibilityRole="button" accessibilityLabel={menuLabel} onPress={onMenu} style={{width: 40, height: 40, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={20} color={theme.colors.textSecondary}/>
      </Focusable>
    </View>
    {skipped !== undefined && <Text testID={`${testID.replace('source', 'skipped')}`} style={[theme.type.rowHelp, {color: theme.colors.amber}]}>{skipped}</Text>}
  </View>;
}

/** No sources yet: a raised card with its title, a line of help and Add source. */
export function LiveSourcesEmpty({title, body, action, testID}: {title: string; body: string; action?: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{backgroundColor: theme.colors.raised, borderRadius: 16, padding: theme.space.md, alignItems: 'center', gap: theme.space.controlGap}}>
    <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{body}</Text>
    {action}
  </View>;
}

/** The Sources heading with when they were last checked at its end. */
export function LiveSourcesHeading({title, checked}: {title: string; checked?: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', paddingHorizontal: Math.max(0, frame.gutter - frame.cardInset)}}>
    <Text accessibilityRole="header" style={[theme.type.sectionLabel, {flex: 1}]}>{title}</Text>
    {checked !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{checked}</Text>}
  </View>;
}

/** A switch row inside the defaults card: its label (and help), the switch at its end. */
export function LiveSwitchRow({label, help, control}: {label: string; help?: string; control: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4}}>
    <View style={{flex: 1, gap: 2}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
      {help !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{help}</Text>}
    </View>
    {control}
  </View>;
}

/** The defaults' raised card of fields. */
export function LiveFieldCard({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.sm - 4, backgroundColor: theme.colors.raised, borderRadius: 14, padding: theme.space.sm - 2}}>{children}</View>;
}
