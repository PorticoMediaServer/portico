import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {Artwork, type ArtworkSource} from '../artwork';
import {LevelMeter} from '../browse/Tracks';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Segmented} from '../controls/Tabs';
import {useFrameMetrics} from '../frames/metrics';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

/** The audio player's queue panel and its rows (desktop-listen "now" and "book-player", tablet-play 9, 10 and 12,
 * final3-tv-12). `AudioPlayerFrame` places the panel (its `panel` region): a raised panel beside the column on a tablet,
 * the list after the controls on TV. The web has the same parts with the same names in `web/src/kit/player`. */

const wide = frameSpec.audio.wide, tvSpec = frameSpec.audio.tv;
type Action = Readonly<{label: string; icon?: IconName; onPress: () => void; testID?: string}>;

/** The panel: segmented tabs at the top (Up next | Lyrics, or Chapters | Up next), the list, and a footer with the
 * count and its action (Save as playlist) or a hint. On TV it is a heading and a count over the list, no panel. */
export function AudioPanel({tabs, heading, footer, children, testID, scrollable = true}: {
  tabs?: Readonly<{items: readonly Readonly<{value: string; label: string; icon: IconName}>[]; value: string; onValueChange: (value: string) => void}>;
  /** TV: "Up next" and "5 songs, 18 min" over the list. */ heading?: Readonly<{title: string; detail?: string}>;
  footer?: Readonly<{text: string; action?: Action}>; children: ReactNode; testID?: string; scrollable?: boolean}) {
  const theme = useTheme(), {form} = useFrameMetrics(), tv = form === 'tv';
  if (tv) return <View testID={testID} style={{flex: 1}}>
    {heading && <View style={{marginBottom: theme.space.sm}}><Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{heading.title}</Text>
      {heading.detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{heading.detail}</Text>}</View>}
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: theme.space.lg}}>{children}</ScrollView>
  </View>;
  return <View testID={testID} style={{flex: 1, borderRadius: wide.panelRadius, overflow: 'hidden', backgroundColor: alpha(theme.colors.ink, .44), borderWidth: theme.space.hairline, borderColor: alpha(theme.colors.text, .08)}}>
    {heading && !tabs && <QueueGroup label={heading.title}/>}
    {tabs && <View style={{margin: theme.space.sm, marginBottom: theme.space.xs}}><Segmented fill variant="glass" value={tabs.value} onValueChange={tabs.onValueChange}
      items={tabs.items.map(item => ({value: item.value, label: <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}><Icon name={item.icon} size={frameSpec.player.pillIcon} color={item.value === tabs.value ? theme.colors.text : theme.colors.textSecondary}/>
        <Text style={[theme.type.rowLabel, { color: item.value === tabs.value ? theme.colors.text : theme.colors.textSecondary}]}>{item.label}</Text></View>}))}/></View>}
    {scrollable ? <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: theme.space.sm}}>{children}</ScrollView> : <View style={{flex: 1, minHeight: 0}}>{children}</View>}
    {footer && <View style={{minHeight: wide.foot, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingLeft: theme.space.md, paddingRight: theme.space.sm, paddingVertical: theme.space.sm, borderTopWidth: theme.space.hairline, borderTopColor: alpha(theme.colors.text, .08)}}>
      <Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textTertiary}]}>{footer.text}</Text>
      {footer.action && <Button testID={footer.action.testID} variant="glass" size="small" icon={footer.action.icon} onPress={footer.action.onPress}>{footer.action.label}</Button>}
    </View>}
  </View>;
}

/** A group's label in the queue ("Now playing", "Next from After Hours", "Added by you") with its hint at the right
 * ("Hold the handle to reorder", "Chapter 15 of 26"). */
export function QueueGroup({label, hint}: {label: string; hint?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), tv = form === 'tv';
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.space.xs, paddingTop: tv ? theme.space.md : theme.space.sm, paddingBottom: tv ? theme.space.controlGap : theme.space.tight, paddingHorizontal: tv ? 0 : theme.space.md}}>
    <Text accessibilityRole="header" style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textTertiary, fontFamily: theme.type.groupLabel.fontFamily}]}>{label}</Text>
    {hint !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{hint}</Text>}
  </View>;
}

/** One queue or chapter row. Its lead says what it is: playing (the level meter), a handle to drag, a finished
 * chapter (a tick) or a chapter number. The row playing is raised and its title is the accent. A title takes a second
 * line rather than being cut. On touch every row ends with its More; a chapter row ends with its length. */
export function QueueRow({lead, artwork, title, subtitle, duration, progress, onPress, more, testID, selected, disabled}: {
  lead: 'playing' | 'handle' | 'done' | number; artwork?: ArtworkSource; title: string; subtitle?: string; duration?: string;
  /** The chapter playing: how far through it is, 0 to 1. */ progress?: number; onPress: () => void; more?: Action; testID: string; selected?: boolean; disabled?: boolean}) {
  const theme = useTheme(), {form} = useFrameMetrics(), tv = form === 'tv', playing = selected ?? lead === 'playing', size = tv ? tvSpec.rowArt : wide.rowArt;
  const glyph = lead === 'playing' ? <LevelMeter/> : lead === 'handle' ? <Icon name="i-grip" size={frameSpec.player.pillIcon} color={theme.colors.textTertiary}/>
    : lead === 'done' ? <Icon name="i-check" size={frameSpec.player.pillIcon} color={theme.colors.textTertiary}/> : <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontFamily: theme.type.groupLabel.fontFamily}]}>{lead}</Text>;
  const end = [theme.type.rowHelp, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums' as const]}];
  return <Focusable testID={testID} focusId={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={[title, subtitle, duration].filter(Boolean).join(', ')} accessibilityState={{selected: playing, disabled}} disabled={disabled} onPress={onPress} focusRadius={theme.baseRadii.control}
    style={{minHeight: tv ? tvSpec.row : wide.row, marginHorizontal: tv ? 0 : theme.space.xs, paddingVertical: theme.space.tight, paddingLeft: tv ? 0 : theme.space.tight, paddingRight: tv ? 0 : theme.space.xs, flexDirection: 'row', alignItems: 'center', gap: tv ? theme.space.sm : theme.space.controlGap, borderRadius: theme.baseRadii.control,
      backgroundColor: playing && !tv ? alpha(theme.colors.text, .1) : 'transparent'}}>
    {!tv && <View style={{width: wide.handle, alignItems: 'center'}}>{glyph}</View>}
    {artwork && <View style={{width: size, height: size, borderRadius: tv ? tvSpec.rowArtRadius : wide.rowArtRadius, overflow: 'hidden', backgroundColor: theme.colors.raisedSoft}}><Artwork source={artwork}/></View>}
    <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline * 2}}>
      <Text style={[theme.type.rowLabel, {color: playing ? theme.colors.accentStrong : theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{title}</Text>
      {(subtitle !== undefined || (duration !== undefined && !!more)) && <Text style={end}>{subtitle}{duration !== undefined && !!more ? `${subtitle !== undefined ? '   ' : ''}${duration}` : ''}</Text>}
      {progress !== undefined && <View style={{height: theme.space.hairline * 3, borderRadius: theme.space.hairline * 2, marginTop: theme.space.hairline * 3, backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.track)}}>
        <View style={{width: `${progress * 100}%`, height: '100%', borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accentStrong}}/></View>}
    </View>
    {duration !== undefined && !more && <Text style={end}>{duration}</Text>}
    {more && <Button testID={more.testID} variant="text" size="small" iconOnly icon="i-more" iconColor={theme.colors.textSecondary} accessibilityLabel={more.label} onPress={more.onPress}/>}
  </Focusable>;
}

/** An audiobook's Speed or Sleep timer on the wide forms (desktop-listen "book-player", tablet-play 12): its name and
 * what it means now ("3 h 39 min left at 1.25×") beside one row of every choice, the chosen one in the accent. */
export function AudioChoiceRow({label, help, choices, testID}: {label: string; help?: string; choices: readonly Readonly<{label: string; selected?: boolean; disabled?: boolean; onPress: () => void}>[]; testID: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    <View style={{width: wide.rowLabel}}><Text style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{label}</Text>
      {help !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{help}</Text>}</View>
    <View accessibilityRole="radiogroup" accessibilityLabel={label} style={{flex: 1, minWidth: 0, flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.hairline * 2, padding: theme.space.hairline * 4, borderRadius: frameSpec.card.radius.phone - 2, backgroundColor: alpha(theme.colors.text, .08)}}>
      {choices.map(choice => <Focusable key={choice.label} testID={`${testID}-${choice.label}`} accessibilityRole="radio" accessibilityState={{checked: !!choice.selected, disabled: choice.disabled}} disabled={choice.disabled} onPress={choice.onPress} focusRadius={theme.baseRadii.control}
        style={{flexGrow: 1, minHeight: theme.spec.segmented.height[theme.column] + theme.space.hairline * 2, paddingHorizontal: theme.space.xs, alignItems: 'center', justifyContent: 'center', borderRadius: theme.baseRadii.control,
          backgroundColor: choice.selected ? alpha(theme.colors.accent, .24) : 'transparent', borderWidth: theme.space.hairline, borderColor: choice.selected ? alpha(theme.colors.accentStrong, .7) : 'transparent'}}>
        <Text style={[theme.type.rowHelp, {color: choice.selected ? theme.colors.text : theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily, fontVariant: ['tabular-nums']}]}>{choice.label}</Text>
      </Focusable>)}
    </View>
  </View>;
}

export function ChapterList({chapters, status, retry, previous, next, testID}: {
  chapters: readonly Readonly<{id: string; number: number; title: string; duration?: string; selected: boolean; disabled: boolean; onPress: () => void}>[];
  status?: string; retry?: Action; previous?: Action & {disabled?: boolean}; next?: Action & {disabled?: boolean}; testID: string;
}) {
  const theme = useTheme();
  return <View testID={testID} style={{flex: 1, minHeight: 0}}>
    {status && <QueueGroup label={status}/>}
    {retry && <Button variant="glass" size="small" icon={retry.icon} onPress={retry.onPress}>{retry.label}</Button>}
    <ScrollView style={{flex: 1}}>{chapters.map(chapter => <QueueRow key={chapter.id} lead={chapter.number} title={chapter.title} duration={chapter.duration}
      selected={chapter.selected} disabled={chapter.disabled} onPress={chapter.onPress} testID={`${testID}-${chapter.id}`}/>)}</ScrollView>
    {previous && next && <View style={{padding: theme.space.sm, flexDirection: 'row', justifyContent: 'space-between', gap: theme.space.xs}}>
      <Button variant="glass" size="small" icon={previous.icon} disabled={previous.disabled} onPress={previous.onPress}>{previous.label}</Button>
      <Button variant="glass" size="small" icon={next.icon} disabled={next.disabled} onPress={next.onPress}>{next.label}</Button>
    </View>}
  </View>;
}

/** Division marks over the audiobook seek track. The seek control owns input. */
export function ChapterTicks({ticks, testID, children}: {ticks: readonly number[]; testID: string; children: ReactNode}) {
  const theme = useTheme(), {form} = useFrameMetrics();
  return <View>
    {children}
    <View testID={testID} pointerEvents="none" style={{position: 'absolute', inset: 0, justifyContent: 'center'}}>
      {ticks.map(tick => <View key={tick} style={{position: 'absolute', left: `${tick * 100}%`, width: theme.space.hairline,
        height: frameSpec.player.track[form], backgroundColor: alpha(theme.colors.ink, .75)}}/>)}
    </View>
  </View>;
}
