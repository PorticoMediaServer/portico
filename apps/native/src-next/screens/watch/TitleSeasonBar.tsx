import React, {useRef, useState} from 'react';
import {Text, View, type LayoutChangeEvent} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Switch} from '../../kit/controls';
import {Menu} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

type Widths = Readonly<{row: number; chip: number; count: number; end: number}>;

/**
 * The episodes bar under a show's tabs, drawn by role as the frames draw it: a viewer gets the season chip, the
 * season's count and Hide watched (phone-app `ep-bar`); whoever has the ⋯ actions gets the chip, the count and ⋯,
 * with Hide watched as the first item inside it (phone-detail `ep-bar`, lead's ruling 13 Nov).
 * The chip's label is never cut: when the row is short the count (the secondary fact) drops first; if it is
 * still short the row wraps, the trailing control going under the chip; a name too long for the row itself
 * ("Unassigned absolute episodes" at the largest text) wraps inside the chip.
 */
export function TitleSeasonBar({label, count, onSeasons, hideWatched, onHideWatched, settings}: {
  label: string; count?: string; onSeasons: () => void;
  hideWatched: boolean; onHideWatched: (value: boolean) => void;
  settings?: Readonly<{label: string; onPress: () => void}>;
}) {
  const theme = useTheme(), t = defaultI18n.t, phone = theme.platform === 'phone', gap = phone ? 8 : 10;
  const more = useRef<View>(null), [open, setOpen] = useState(false);
  const [widths, setWidths] = useState<Widths>({row: 0, chip: 0, count: 0, end: 0});
  const measure = (key: keyof Widths) => (event: LayoutChangeEvent) => {
    const width = event.nativeEvent.layout.width;
    setWidths(current => current[key] === width ? current : {...current, [key]: width});
  };
  const switchRow = !theme.isTV && !settings, end = switchRow || !!settings;
  // Every part keeps its natural width (the count is still measured while dropped), so this never flips back and forth.
  const dropCount = !!count && widths.row > 0 && widths.chip + gap + widths.count + (end ? gap + widths.end : 0) > widths.row;
  return <View testID="watch-season-bar" onLayout={measure('row')} style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap}}>
    <Button testID="watch-seasons-open" variant="glass" size="small" trailingIcon="i-chdown" labelLines={2} onLayout={measure('chip')}
      accessibilityLabel={dropCount ? `${label}, ${count}` : undefined} style={{flexShrink: 0, maxWidth: '100%'}} onPress={onSeasons}>{label}</Button>
    {count ? <Text testID="watch-season-count" onLayout={measure('count')} numberOfLines={1} accessibilityElementsHidden={dropCount} importantForAccessibility={dropCount ? 'no-hide-descendants' : 'auto'}
      style={[theme.type.caption, {color: theme.colors.textTertiary, flexShrink: 0}, dropCount ? {position: 'absolute', opacity: 0} : null]}>{count}</Text> : null}
    {end && <View testID="watch-season-end" onLayout={measure('end')} style={{marginLeft: 'auto', flexShrink: 0}}>
      {switchRow ? <View testID="watch-season-controls" style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary}]}>{t('next.controls.hideWatched')}</Text>
        <Switch testID="watch-hide-watched" variant={phone ? 'episode' : 'selection'} checked={hideWatched} accessibilityLabel={t('next.controls.hideWatched')} onCheckedChange={onHideWatched}/>
      </View> : <Button ref={more} testID="watch-show-settings-open" variant="icon" icon="i-more" iconOnly accessibilityLabel={settings!.label} accessibilityState={{expanded: open}} onPress={() => setOpen(true)}/>}
    </View>}
    {settings && <Menu testID="watch-season-menu" title={settings.label} open={open} onOpenChange={setOpen} anchorRef={more} returnFocusRef={more} items={[
      {id: 'hide-watched', icon: 'i-eyeoff', label: t('next.controls.hideWatched'), selected: hideWatched, onSelect: () => onHideWatched(!hideWatched)},
      {id: 'show-settings', icon: 'i-filter', label: t('title.showSettings'), onSelect: settings.onPress},
    ]}/>}
  </View>;
}
