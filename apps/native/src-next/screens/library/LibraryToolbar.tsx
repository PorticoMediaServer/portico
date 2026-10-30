import React, {type ReactNode, type RefObject} from 'react';
import {ScrollView, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import type {BrowseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {Button, Chip} from '../../kit/controls';
import {FocusItem} from '../../kit/focus';
import {FiltersButton, QuickToggle, SaveViewButton, type BrowseLabels} from '../../kit/browse';
import {useTheme} from '../../kit/theme';
import {LibrarySortMenu} from './LibrarySortMenu';

/** Phone4/tablet3–4 and tv4 keep their drawn control order. Selection is an
 * actual caller action; an absent execution port never creates a dummy button.
 * tablet4: a docked Filters panel carries the quick play states as its own
 * fields, so the toolbar drops them while it is docked. */
export function LibraryToolbar({focusScope, model, labels, filterCount, filtersOpen, onFilters, onSort, onReshuffle, onQuick, onSave,
  onJump, jumpEnabled, jumpAnchor, onSelect, selecting, canSave, docked = false, showSelect = true, trailing, gutter}: {
  focusScope: string; model: BrowseToolbarModel; labels: BrowseLabels; filterCount: number; filtersOpen: boolean;
  onFilters: () => void; onSort: (sort: BrowseSortSelection) => void; onReshuffle: () => void;
  onQuick: (id: string, enabled: boolean) => void; onSave: () => void;
  canSave: boolean;
  onJump: () => void; jumpEnabled: boolean; jumpAnchor: RefObject<View | null>;
  onSelect?: () => void; selecting: boolean;
  /** True while the tablet's Filters panel is docked beside the grid. */
  docked?: boolean;
  /** Expanded draws Select in the library header, not in this row. */
  showSelect?: boolean;
  /** The drawn right group: the view's real count and the grid/list control. */
  trailing?: ReactNode;
  /** The drawn tablet gutter, so the row lines up with the cards. */
  gutter?: number;
}) {
  const theme = useTheme(), {width} = useWindowDimensions();
  const sort = <LibrarySortMenu focusScope={focusScope} sorts={model.sorts} value={model.sort} label={labels.sort} onValueChange={onSort} onReshuffle={onReshuffle}/>;
  const filters = <FocusItem focusId="filters"><FiltersButton count={filterCount} open={filtersOpen} onPress={onFilters}/></FocusItem>;
  const controls = <>
    {theme.isTV ? <>{sort}{filters}<Chip ref={jumpAnchor} focusId="jump" disabled={!jumpEnabled} onPress={onJump}>{defaultI18n.t('next.browse.native.jumpTo')}</Chip></>
      : <>{width >= 600 && onSelect && !selecting && showSelect && <FocusItem focusId="select"><Button testID="library-select" variant="glass" size="small" onPress={onSelect} style={{height: 44}}>{defaultI18n.t('next.selection.select')}</Button></FocusItem>}{filters}{sort}</>}
    {theme.isTV && <View style={{width: 1.5, height: 40, backgroundColor: theme.colors.line, marginHorizontal: 6}}/>}
    {!docked && model.quick.map(preset => <FocusItem key={preset.id} focusId={`quick:${preset.id}`}><QuickToggle checked={preset.active} onCheckedChange={enabled => onQuick(preset.id, enabled)}>{preset.label?.text ?? labels.quick(preset)}</QuickToggle></FocusItem>)}
    {theme.isTV && <View style={{marginLeft: 'auto'}}><FocusItem focusId="save"><SaveViewButton canSave={canSave} onSave={onSave}/></FocusItem></View>}
  </>;
  if (theme.isTV) return <View style={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: 16, flexDirection: 'row', alignItems: 'center', gap: 14}}>{controls}</View>;
  // tablet4/5: one drawn toolbar row, with the real count and the grid/list
  // control at the right. The phone keeps its scrolling strip.
  if (theme.platform !== 'phone') {
    // Select lives in the drawn library header on both tablet sizes.
    return <View style={{paddingHorizontal: gutter ?? theme.gutter, paddingTop: 10, flexDirection: 'row', alignItems: 'center', gap: 8}}>
      {filters}{sort}
      <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8}}>{trailing}</View>
    </View>;
  }
  return <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: 10, flexDirection: 'row', alignItems: 'center', gap: 8}}>{controls}</ScrollView>;
}
