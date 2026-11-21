import React, {type ReactNode, type Ref, type ComponentProps} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {ChipRail} from '../browse/Toolbar';
import {Button, Chip, Segmented, Select} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {Icon} from '../Icon';
import {TVRow} from '../layout/TVRow';
import {useWholeItems} from '../layout/ListEdgeFade';
import {Banner} from '../overlays';
import {NativeLivePhonePicker} from './LivePhonePicker';
import {useTheme} from '../theme';

export function GuideViewSwitch({value, onChange, list, guide, compact}: {
  value: 'list' | 'guide'; onChange: (value: 'list' | 'guide') => void; list: string; guide: string; compact: boolean;
}) {
  const theme = useTheme();
  return <Segmented testID="live-view" variant={compact ? 'phone' : 'glass'} value={value}
    onValueChange={next => {if (next === 'list' || next === 'guide') onChange(next);}}
    itemProps={item => ({accessibilityLabel: item.value === 'list' ? list : guide})}
    items={(compact ? ['list', 'guide'] as const : ['guide', 'list'] as const).map(mode => ({value: mode, label: <View style={{flexDirection: 'row', gap: theme.space.xs, alignItems: 'center'}}>
      <Icon name={mode === 'list' ? 'i-list' : 'i-timeline'} size={theme.type.rowLabel.fontSize} color={value === mode ? theme.colors.text : theme.colors.textTertiary}/>
      {compact && <Text style={[theme.type.rowLabel, {color: value === mode ? theme.colors.text : theme.colors.textTertiary}]}>{mode === 'list' ? list : guide}</Text>}
    </View>}))}/>;
}

export function GuideFilterStrip({choices, leading, region}: {
  choices: readonly Readonly<{id: string; testID: string; label: string; count?: number; selected: boolean; onPress: () => void}>[];
  leading?: ReactNode; region: FocusRegionProps;
}) {
  const theme = useTheme(), focusRoom = theme.isTV ? theme.space.sm : 0;
  const whole = useWholeItems('x', 'fit', choices.map(choice => choice.id), focusRoom, theme.space.sm);
  // TV: whole chips only, never one cut at the row's end (owner, 20 Nov 20:30). A TV has a handful of groups, so a plain row.
  // The frame sets the row on the page's gutters (tv.html 6: All starts under the title's first letter); the row reaches
  // out by the focus ring's room on each side so a focused chip's ring is not clipped.
  if (theme.isTV) return <FocusRegion {...region} {...whole.container} style={{marginHorizontal: -focusRoom}}><TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} {...whole.scroll}
    contentContainerStyle={{gap: theme.space.xs, paddingLeft: focusRoom, paddingRight: focusRoom + whole.pad, paddingVertical: focusRoom}}>
    {leading}
    {choices.map(item => <View key={item.id} {...whole.item(item.id)}><Chip focusId={item.id} testID={item.testID} count={item.count} selected={item.selected}
      onPress={item.onPress} onFocus={() => whole.focus(item.id)}>{item.label}</Chip></View>)}
  </ScrollView></TVRow></FocusRegion>;
  // Touch: the browse toolbar's chip rail, so the owner's rules hold here too (20 Nov): on a phone the next chip shows partly
  // under a fade at the row's end; on a tablet the row stops on whole chips. A plain list cut a chip at the screen edge.
  return <FocusRegion {...region}><View style={{flexDirection: 'row', paddingHorizontal: theme.gutter}}><ChipRail>
    {leading}{choices.map(item => <Chip key={item.id} focusId={item.id} testID={item.testID} count={item.count} selected={item.selected} onPress={item.onPress}>{item.label}</Chip>)}
  </ChipRail></View></FocusRegion>;
}

export function GuideActionRow({children, region, testID}: {children: ReactNode; region?: FocusRegionProps; testID?: string}) {
  const theme = useTheme();
  const style = {flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: theme.space.xs, alignItems: 'center' as const};
  return region ? <FocusRegion {...region} testID={testID} style={style}>{children}</FocusRegion> : <View testID={testID} style={style}>{children}</View>;
}

export function GuideOnNow({label, clock}: {label: string; clock?: string}) {
  const theme = useTheme();
  return <GuideActionRow><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{clock}</Text></GuideActionRow>;
}

export function GuideNotices({reason, stale}: {reason?: Readonly<{title: string; body: string}>; stale?: string}) {
  const theme = useTheme();
  if (!reason && !stale) return null;
  return <View testID="live-guide-notices" style={{gap: theme.space.xs, marginBottom: theme.space.sm, paddingHorizontal: theme.isTV ? 0 : theme.gutter}}>
    {reason && <Banner testID="live-server-wide" placement="inline" variant="quiet" tone="warning" icon="i-alert"
      message={<Text><Text style={theme.type.rowLabel}>{reason.title}</Text>{' '}{reason.body}</Text>}
      accessibilityLabel={`${reason.title} ${reason.body}`}/>}
    {stale && <Banner testID="live-guide-stale" placement="inline" variant="quiet" tone="warning" icon="i-clock" message={stale}/>}
  </View>;
}

export function GuideRecordingsButton({label, count, onPress}: {label: string; count: number; onPress: () => void}) {
  const theme = useTheme();
  return <View><Button testID="live-recordings" variant="icon" size="small" icon="i-rec-ring" accessibilityLabel={label} onPress={onPress}/>
    {count > 0 && <View testID="live-recordings-conflict" pointerEvents="none" style={{position: 'absolute', top: theme.space.xs, right: theme.space.xs,
      width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs, backgroundColor: theme.colors.record}}/>}</View>;
}

export function GuideSourcePicker({label, value, options, current, phone, onChange}: {
  label: string; value?: string; options: readonly {value: string; label: string}[]; current?: string; phone: boolean; onChange: (value: string) => void;
}) {
  const theme = useTheme(), style = {minHeight: theme.targets.touch - theme.space.xs, borderWidth: theme.space.hairline,
    borderColor: theme.colors.lineStrong, borderRadius: theme.baseRadii.control, paddingHorizontal: theme.space.sm, justifyContent: 'center' as const};
  if (!phone) return <Select focusId="live-source" testID="live-source" accessibilityLabel={label} value={value} options={options} onValueChange={onChange}/>;
  if (current) return <View testID="live-source" style={style}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{current}</Text></View>;
  return <NativeLivePhonePicker testID="live-source" style={style} accessibilityLabel={label} value={value ?? ''}
    options={options.map(option => ({id: option.value, label: option.label}))} onChange={onChange}>{options.find(option => option.value === value)?.label}</NativeLivePhonePicker>;
}

export function GuideDayPicker({name, date, ...props}: Omit<ComponentProps<typeof NativeLivePhonePicker>, 'children'> & {name: string; date?: string}) {
  const theme = useTheme();
  return <NativeLivePhonePicker {...props}>
    <Icon name="i-cal" size={theme.type.rowLabel.fontSize} color={theme.colors.textTertiary}/>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{name}</Text>
    {date && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{date}</Text>}
  </NativeLivePhonePicker>;
}

export function GuideViewport({children, viewport, onLayout, leading, trailing}: {
  children: ReactNode; viewport: Ref<View>; onLayout: NonNullable<ComponentProps<typeof View>['onLayout']>; leading: number; trailing: number;
}) {
  return <View ref={viewport} onLayout={onLayout} style={{flex: 1, paddingLeft: leading, paddingRight: trailing}}>{children}</View>;
}
