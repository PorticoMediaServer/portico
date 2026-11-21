import React from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {Artwork, type ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

const t = defaultI18n.t;

export type QueueRowProps = Readonly<{
  testID: string;
  menuTestID?: string;
  focusId?: string;
  title: string;
  /** The second line: the artist (and on touch, the album). Wraps; never cut. */
  detail?: readonly string[];
  seconds?: number;
  source?: ArtworkSource;
  /** The row playing now: says so in place of a play action. */
  current?: boolean;
  disabled?: boolean;
  /** TV: a row nobody may play stays a focus stop so the list can still be read. */
  focusableWhenDisabled?: boolean;
  onPress?: () => void;
  onLongPress?: () => void;
  onFocus?: () => void;
  /** Touch: a ⋯ at the row's end. */
  onMenu?: () => void;
}>;

/** One row of Up next, on every form (phone-app 14 sheet, tablet-play 9, tv-7): the cover, the title and its
 * second line wrapped in full, and the song's length as a clock ("2:40"). The row's sizes are the audio frame's. */
export function QueueRow({testID, menuTestID, focusId, title, detail, seconds, source, current, disabled, focusableWhenDisabled, onPress, onLongPress, onFocus, onMenu}: QueueRowProps) {
  const theme = useTheme();
  const spec = theme.isTV ? frameSpec.audio.tv : frameSpec.audio.wide;
  const label = current ? title : t('player.queuePlay', {title});
  const body = ({focused}: {focused: boolean}) => ({
    flex: 1,
    minHeight: spec.row,
    paddingVertical: theme.space.tight,
    paddingHorizontal: theme.space.sm,
    flexDirection: 'row' as const,
    alignItems: 'center' as const,
    gap: theme.space.sm,
    borderRadius: theme.baseRadii.control,
    // tablet-play 8 `.q.now`: the row playing now has the neutral fill (text at .1) on a touch screen.
    backgroundColor: focused ? theme.colors.raisedHigh : current && !theme.isTV ? alpha(theme.colors.text, .1) : 'transparent',
  });
  const row = <Focusable testID={testID} focusId={focusId} focusKind="row" focusRadius={theme.baseRadii.control}
    accessibilityRole={onPress ? 'button' : 'text'} accessibilityLabel={[label, ...(detail ?? [])].join(', ')}
    disabled={disabled || (!onPress && !onLongPress)} focusableWhenDisabled={focusableWhenDisabled}
    onFocus={onFocus} onPress={onPress} onLongPressAction={onLongPress} style={body}>
    {source && <Artwork source={source} width={spec.rowArt} height={spec.rowArt}
      style={{borderRadius: spec.rowArtRadius, overflow: 'hidden', flexShrink: 0}}/>}
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: current ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
      {!!detail?.length && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail.join(' · ')}</Text>}
    </View>
    {current && theme.isTV && <Text style={[theme.type.caption, {color: theme.colors.accentStrong}]}>{t('player.queueNowPlaying')}</Text>}
    {!!seconds && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>
      {clockText(seconds)}
    </Text>}
  </Focusable>;
  if (!onMenu) return row;
  return <View style={{flexDirection: 'row', alignItems: 'center'}}>
    {row}
    <Focusable testID={menuTestID} focusKind="button" accessibilityRole="button"
      accessibilityLabel={t('server.row.actions', {name: title})} onPress={onMenu}
      style={{width: theme.targets.touch, height: theme.targets.touch, alignItems: 'center', justifyContent: 'center'}}>
      <Icon name="i-more" size={theme.scale.icon.tertiary.size} color={theme.colors.textSecondary}/>
    </Focusable>
  </View>;
}

/** A group's label in Up next ("Now playing", "From Abbey Road", "Added by you"): tablet-play 8 `.q-sec`, 13/700 in the quiet
 * colour, a small label over the rows rather than a heading (TV's caption and group label are both 20, so TV does not move). */
export function QueueGroupLabel({children}: {children: string}) {
  const theme = useTheme();
  return <Text accessibilityRole="header" style={[theme.type.captionStrong, {fontFamily: theme.type.groupLabel.fontFamily, fontWeight: theme.type.groupLabel.fontWeight,
    color: theme.colors.textTertiary, paddingHorizontal: theme.space.sm, paddingTop: theme.space.sm, paddingBottom: theme.space.tight,
  }]}>{children}</Text>;
}
