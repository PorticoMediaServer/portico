import React, {type Ref} from 'react';
import {View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {Button, ResumeButton} from './Button';

/** One button after the primary. */
export type RowAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; testID?: string;
  /** Shown with its label where there is room (a tablet on its side and TV): Start over, My List, Shuffle. Round elsewhere. */
  labelled?: boolean;
  /** On (in My List, watched, a favourite): tinted in the accent (phone-detail d1 frame 3). */
  active?: boolean;
  /** Also on a title page's collapsed bar, after the primary (desktop-detail f3: Play and +). */
  pinned?: boolean;
  /** Drawn only on a tablet on its side and TV. On a phone and an upright tablet it is in More, so the primary keeps the
   * room for its label (phone-detail d1: Play, My List, Download, More; Watched is in More). */
  wide?: boolean}>;
/** The one flexible button. With `progress` it is drawn as Resume, the played part along its lower edge and the time
 * left beside its label; with `startOver` that is the button after it. */
export type RowPrimary = Omit<RowAction, 'labelled' | 'pinned' | 'wide'> & Readonly<{progress?: number; remaining?: string; startOver?: Readonly<{label: string; onPress: () => void}>;
  /** The focus id the page starts on. */
  focusId?: string}>;
export type ActionRowProps = Readonly<{primary?: RowPrimary; actions?: readonly RowAction[];
  /** The primary's own view: a TV title page's frame points a focus guide at it (tvOS moves only to what is on screen). */
  primaryRef?: Ref<View>;
  /** The page is still loading: every button keeps its place, none can be pressed, and the primary can still take focus. */
  loading?: boolean}>;

/**
 * A row of buttons, primary first, as the mock-ups draw it per form (`frameSpec.actionRow`). Never more than one line:
 * - a phone (phone-detail d1 to d3): the primary takes the room the round buttons leave; every other button is a round
 *   glass disc of the hero button's height; a `wide` action is not drawn (it is in More);
 * - an upright tablet (tablet f8): the same, centred, with the primary at its own width;
 * - a tablet on its side (tablet f6, f15): from the start, the primary, Start over, then each action, labelled where it
 *   says so; a label that does not fit takes a second line inside its button, it is never cut;
 * - TV (tv-detail 1, 2): as a tablet on its side, one line at the mock-ups' 72 high with the gap of 18, except that an
 *   extra (`wide`) action such as Watched is a round disc there, like More: only Start over and My List carry words.
 */
export function ActionRow({primary, actions = [], loading = false, primaryRef}: ActionRowProps) {
  const theme = useTheme(), spec = frameSpec.actionRow, {form} = theme;
  const stacked = form === 'phone' || form === 'medium', height = theme.spec.button.hero.height[theme.column];
  const side = (action: RowAction) => action.labelled && !stacked && !(form === 'tv' && action.wide)
    ? <Button key={action.label} variant="glass" size="hero" icon={action.icon} iconColor={action.active ? theme.colors.accentStrong : undefined} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}>{action.label}</Button>
    : <Button key={action.label} variant="glass" size="hero" iconOnly icon={action.icon} accessibilityLabel={action.label} accessibilityState={{selected: action.active}} iconColor={action.active ? theme.colors.accentStrong : undefined} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}
      style={[{width: height, height}, action.active && {backgroundColor: alpha(theme.colors.accentStrong, theme.spec.card.progress.trackAlpha), borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, spec.activeEdge)}]}/>;
  const width = spec.primaryWidth[form];
  const lead = primary && {icon: primary.icon, disabled: primary.disabled || loading, onPress: primary.onPress, testID: primary.testID, focusId: primary.focusId ?? primary.testID,
    focusableWhenDisabled: loading, align: stacked ? 'stretch' as const : 'start' as const, style: form === 'phone' ? {flex: 1} : width ? {width} : undefined};
  // While loading the row keeps its buttons but says nothing it does not know yet (the time left).
  return <View style={{minHeight: height, alignSelf: stacked ? 'stretch' : undefined, flexDirection: 'row', alignItems: 'center', justifyContent: stacked ? 'center' : 'flex-start', gap: spec.gap[form]}}>
    {lead && (primary.progress !== undefined && !loading
      ? <ResumeButton {...lead} ref={primaryRef} size="hero" progress={primary.progress} remaining={primary.remaining}>{primary.label}</ResumeButton>
      : <Button {...lead} ref={primaryRef} variant="primary" size="hero">{primary.label}</Button>)}
    {primary?.startOver && !loading && <Button variant="glass" size="hero" icon="i-restart" onPress={primary.startOver.onPress}>{primary.startOver.label}</Button>}
    {actions.filter(action => !action.wide || !stacked).map(side)}
  </View>;
}
