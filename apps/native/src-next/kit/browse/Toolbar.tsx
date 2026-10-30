import React, {useRef, useState, type ReactNode} from 'react';
import {ScrollView, useWindowDimensions, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {BrowseSortCapability, BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {sortDirectionKind} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import type {BrowseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {Button, Chip, Segmented, Focusable} from '../controls';
import {MenuRow, Popover} from '../overlays';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {alpha, browsePlatform, browseType} from './presentation';
import type {AppliedFilterChip, BrowsePlatform, BrowseToolbarProps} from './types';

const t = defaultI18n.t;

export function FiltersButton({count = 0, onPress, open = false}: {count?: number; onPress: () => void; open?: boolean}) {
  const theme = useTheme(), tv = theme.isTV, phone = theme.platform === 'phone';
  return <Focusable focusKind="chip" focusRadius={tv ? 14 : 10} accessibilityRole="button" onPress={onPress} accessibilityLabel={count ? t('next.browse.native.filtersCount', {count}) : t('next.browse.native.filters')} accessibilityState={{expanded: open}} style={{height: tv ? 64 : phone ? 34 : 40, paddingHorizontal: tv ? 26 : 14, borderRadius: tv ? 14 : 10, flexDirection: 'row', alignItems: 'center', gap: tv ? 10 : 8, backgroundColor: open ? tv ? alpha(theme.colors.text, .2) : theme.colors.raisedBright : tv ? alpha(theme.colors.text, .08) : theme.colors.raisedHigh}}>
    <Icon name="i-filter" size={tv ? 26 : phone ? 17 : 18} color={theme.colors.textTertiary}/><Text style={[browseType(theme, tv ? 22 : 14.5, 700), {color: tv && !open ? theme.colors.textSecondary : theme.colors.text}]}>{t('next.browse.native.filters')}</Text>
    {count > 0 && <View style={{minWidth: tv ? 32 : 21, height: tv ? 32 : 21, paddingHorizontal: tv ? 8 : 6, borderRadius: tv ? 16 : 11, backgroundColor: tv ? theme.colors.accent : theme.colors.accentStrong, alignItems: 'center', justifyContent: 'center'}}><Text style={[browseType(theme, tv ? 18 : 12, 800), {color: theme.colors.actionInk}]}>{count}</Text></View>}
  </Focusable>;
}
export function QuickToggle({children, checked, onCheckedChange}: {children: ReactNode; checked: boolean; onCheckedChange: (checked: boolean) => void}) {
  return <Chip variant="quick" selected={checked} onPress={() => onCheckedChange(!checked)} accessibilityRole="checkbox" accessibilityState={{checked}}>{children}</Chip>;
}
export function SaveViewButton({canSave, onSave}: {canSave: boolean; onSave: () => void}) {
  return <Button variant="text" size="compact" icon="i-save" disabled={!canSave} onPress={onSave}>{t('next.browse.native.saveView')}</Button>;
}
export function ViewToggle({value, onValueChange, itemProps}: {itemProps?: (value: 'grid' | 'list') => Pick<React.ComponentProps<typeof Focusable>, 'focusId' | 'testID'>; value: 'grid' | 'list'; onValueChange: (value: 'grid' | 'list') => void}) {
  const theme = useTheme(), phone = theme.platform === 'phone';
  return <View accessibilityRole="radiogroup" style={{flexDirection: 'row', padding: phone ? 0 : 3, borderRadius: 11, backgroundColor: phone ? 'transparent' : theme.colors.raisedHigh}}>{(['grid', 'list'] as const).map(choice => <Focusable key={choice} focusId={`view-${choice}`} {...itemProps?.(choice)} accessibilityRole="radio" accessibilityLabel={t(choice === 'grid' ? 'next.browse.native.grid' : 'next.browse.native.list')} accessibilityState={{checked: value === choice}} onPress={() => onValueChange(choice)} focusKind="row" focusRadius={8} style={{width: phone ? 32 : 38, height: phone ? 30 : 34, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: value === choice ? phone ? theme.colors.raisedHigh : theme.colors.raisedBright : 'transparent'}}><Icon name={choice === 'grid' ? 'i-grid' : 'i-list'} size={20} color={value === choice ? theme.colors.text : theme.colors.textTertiary}/></Focusable>)}</View>;
}
export function CardSizeButton({value, options, onValueChange}: NonNullable<BrowseToolbarProps['cardSize']>) {
  const anchorRef = useRef<View>(null), [open, setOpen] = useState(false);
  return <>
    <Button ref={anchorRef} variant="icon" size="compact" iconOnly icon="i-size" accessibilityLabel={t('next.browse.native.cardSize')} accessibilityState={{expanded: open}} onPress={() => setOpen(!open)}/>
    <Popover open={open} onOpenChange={setOpen} anchorRef={anchorRef} returnFocusRef={anchorRef} title={t('next.browse.native.cardSize')} placement="bottom-end">
      {options.map(option => <MenuRow key={option.value} label={option.label} selected={option.value === value} accessibilityRole="radio" accessibilityState={{checked: option.value === value}} onPress={() => {onValueChange(option.value); setOpen(false);}}/>)}
    </Popover>
  </>;
}

export function SortMenu({scope, focusId, sorts, value, label, onValueChange, onReshuffle, open: suppliedOpen, onOpenChange}: {
  scope?: string; /** The trigger's stable id inside a focus region. */ focusId?: string; sorts: readonly BrowseSortCapability[]; value: BrowseToolbarModel['sort']; label: (sort: BrowseSortCapability) => string;
  onValueChange: (value: BrowseSortSelection) => void; onReshuffle?: () => void; open?: boolean; onOpenChange?: (open: boolean) => void;
}) {
  const theme = useTheme(), anchorRef = useRef<View>(null), [localOpen, setLocalOpen] = useState(false);
  const open = suppliedOpen ?? localOpen, active = sorts.find(sort => sort.id === value.field);
  if (value.reshufflable && !onReshuffle) throw new Error('Random browse sorting needs the host reshuffle callback.');
  const direction = (dir: 'asc' | 'desc') => t('web.filters.direction', {kind: sortDirectionKind(value.field), dir});
  const setOpen = (next: boolean) => {if (suppliedOpen === undefined) setLocalOpen(next); onOpenChange?.(next);};
  const select = (choice: BrowseSortCapability) => {
    if (choice.id === value.field && value.reshufflable && onReshuffle) onReshuffle();
    else {
      const flipped = value.direction === 'asc' ? 'desc' : 'asc';
      onValueChange({field: choice.id, direction: choice.id === value.field && choice.directions.includes(flipped) ? flipped : choice.defaultDirection});
    }
    setOpen(false);
  };
  return <>
    <Focusable ref={anchorRef} focusId={focusId} focusKind="chip" focusRadius={theme.isTV ? 14 : 10} accessibilityRole="button" accessibilityLabel={t('next.browse.native.sort')} accessibilityState={{expanded: open}} onPress={() => setOpen(!open)} style={{height: theme.isTV ? 64 : theme.platform === 'phone' ? 34 : 40, borderRadius: theme.isTV ? 14 : 10, paddingHorizontal: theme.isTV ? 26 : 14, flexDirection: 'row', alignItems: 'center', gap: 8, backgroundColor: theme.isTV ? alpha(theme.colors.text, .08) : theme.colors.raisedHigh}}>
      <Icon name="i-sort" size={theme.isTV ? 26 : 18} color={theme.colors.textTertiary}/>
      <View style={{flexDirection: 'row', gap: 7, alignItems: 'center'}}>
        {active && <Text style={[browseType(theme, theme.isTV ? 22 : 14.5, 700), {color: theme.colors.text}]}>{label(active)}</Text>}
        {active && value.flippable && !active.seeded && <Text style={[browseType(theme, theme.isTV ? 22 : 14.5), {color: theme.colors.textTertiary}]}>{direction(value.direction)}</Text>}
        {!theme.isTV && <Icon name="i-down" size={16} color={theme.colors.textTertiary}/>}
      </View>
    </Focusable>
    <Popover testID={scope ? `${scope}.sort-menu` : undefined} open={open} onOpenChange={setOpen} anchorRef={anchorRef} returnFocusRef={anchorRef} title={t('next.browse.native.sort')} width={theme.isTV ? 560 : 300} placement="bottom-end">
      <ScrollView style={{maxHeight: theme.isTV ? 680 : 560}} keyboardShouldPersistTaps="handled">{sorts.map(choice => <MenuRow key={choice.id} focusId={`sort:${choice.id}`} label={label(choice)} description={choice.expensive ? t('next.browse.native.expensiveSort') : undefined} selected={choice.id === value.field} accessibilityRole="radio" accessibilityState={{checked: choice.id === value.field}} variant={theme.isTV ? 'tv' : 'compact'} style={{minHeight: theme.isTV ? 64 : 44}} onPress={() => select(choice)}/>)}</ScrollView>
      {active && value.flippable && !active.seeded && <View style={{borderTopWidth: 1, borderTopColor: theme.colors.line, padding: 8, gap: 6}}>
        <Text style={[browseType(theme, theme.isTV ? 20 : 13, 700), {color: theme.colors.textTertiary}]}>{t('next.browse.native.order')}</Text>
        <Segmented itemProps={item => ({focusId: `order:${item.value}`})} variant="browse" value={value.direction} onValueChange={dir => onValueChange({field: value.field, direction: dir as 'asc' | 'desc'})} items={active.directions.map(dir => ({value: dir, label: direction(dir)}))}/>
      </View>}
    </Popover>
  </>;
}

export function AppliedFilterChips({filters, onRemove, onClear, platform, children}: {filters: readonly AppliedFilterChip[]; onRemove: (id: string) => void; onClear?: () => void; platform?: BrowsePlatform; children?: ReactNode}) {
  const theme = useTheme(), target = browsePlatform(theme, platform);
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: target === 'tv' ? 12 : 8, flexWrap: 'wrap'}}>
    {filters.map(({id, parts}) => <Focusable key={id} accessibilityRole="button" focusKind="chip" focusRadius={target === 'tv' ? 14 : 17} onPress={() => onRemove(id)} accessibilityLabel={t('next.browse.native.removeFilter', {field: parts.field.text})} style={{minHeight: target === 'tv' ? 52 : target === 'phone' ? 30 : 34, paddingLeft: target === 'tv' ? 20 : 11, paddingRight: 7, borderRadius: target === 'tv' ? 14 : 17, flexDirection: 'row', alignItems: 'center', gap: 7, backgroundColor: target === 'tv' ? alpha(theme.colors.accent, .18) : theme.colors.raisedBright}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: target === 'tv' ? 8 : 7, flexWrap: 'wrap'}}>
        <Text style={[browseType(theme, target === 'tv' ? 20 : 13, 600), {color: theme.colors.textTertiary}]}>{parts.field.text}</Text>
        {!['equals', 'in', 'contains-any'].includes(parts.operator) && <Text style={[browseType(theme, target === 'tv' ? 20 : 13, 600), {color: theme.colors.textSecondary}]}>{parts.operatorPart.text}</Text>}
        {parts.values.map(part => <Text key={part.id} style={[browseType(theme, target === 'tv' ? 20 : 13, 700), {color: theme.colors.text}]}>{part.text}</Text>)}
      </View>
      <Icon name="i-close" size={target === 'tv' ? 22 : 14} color={theme.colors.textSecondary}/>
    </Focusable>)}
    {filters.length > 0 && onClear && <Button variant="text" size="small" onPress={onClear}>{t('next.browse.native.clearAll')}</Button>}
    {children}
  </View>;
}

/** The host pins this measured block above its windowed grid; it never owns scrolling data. */
export function BrowseToolbar({model, platform, sticky = false, resultCount, onQuickChange, onFilters, filtersOpen = false, onSortChange, onReshuffle, labels, applied = [], onRemoveFilter, onClearFilters, view, cardSize, save, onJump, onSelect, selection, children, testID}: BrowseToolbarProps) {
  const theme = useTheme(), {width} = useWindowDimensions(), target = browsePlatform(theme, platform), phone = target === 'phone', tv = target === 'tv';
  if (applied.length > 0 && !onRemoveFilter) throw new Error('Applied browse filters need a real remove callback.');
  if (selection) return <View testID={testID}>{selection}</View>;
  const sort = <SortMenu sorts={model.sorts} value={model.sort} label={labels.sort} onValueChange={onSortChange} onReshuffle={onReshuffle}/>;
  const filters = <FiltersButton count={model.applied.length} open={filtersOpen} onPress={onFilters}/>;
  const quick = model.quick.map(item => <QuickToggle key={item.id} checked={item.active} onCheckedChange={checked => onQuickChange(item.id, checked)}>{item.label?.text ?? labels.quick(item)}</QuickToggle>);
  const count = <Text accessibilityLiveRegion="polite" style={[browseType(theme, tv ? 22 : phone ? 13 : 14.5, 600), {color: phone || tv ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{resultCount}</Text>;
  return <View testID={testID} accessibilityLabel={t('next.browse.native.toolbar')} style={{zIndex: sticky ? theme.layers.sticky : undefined, backgroundColor: sticky ? alpha(theme.colors.ink, .86) : undefined, paddingTop: phone ? 10 : sticky ? 12 : 20, paddingBottom: sticky ? 12 : 0}}>
    {phone ? <>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: 20, gap: 8, alignItems: 'center'}}>{filters}{sort}{onSelect && width >= 600 && <Button variant="glass" size="small" onPress={onSelect}>{t('next.selection.select')}</Button>}{quick}{children}</ScrollView>
      <View style={{paddingHorizontal: 20, paddingTop: 10, paddingBottom: 10, flexDirection: 'row', alignItems: 'center', gap: 10}}>
        {count}<View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8}}>{view && <ViewToggle {...view}/>} {cardSize && <CardSizeButton {...cardSize}/>} {save && <SaveViewButton {...save}/>}</View>
      </View>
    </> : <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 14 : 8, paddingHorizontal: theme.gutter}}>
      {tv ? <>{sort}{filters}{onJump && <Chip onPress={onJump}>{t('next.browse.native.jumpTo')}</Chip>}</> : <>{filters}{onSelect && width >= 600 && <Button variant="glass" size="small" onPress={onSelect}>{t('next.selection.select')}</Button>}</>}
      <View style={{height: tv ? 40 : 24, width: tv ? 1.5 : 1, marginHorizontal: 6, backgroundColor: theme.colors.line}}/>
      {quick}{children}
      <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8}}>
        {!tv && <>{count}{sort}{view && <ViewToggle {...view}/>} {cardSize && <CardSizeButton {...cardSize}/>}</>}
        {save && <SaveViewButton {...save}/>}
      </View>
    </View>}
    {(applied.length > 0 || tv) && <View style={{paddingHorizontal: phone ? 20 : theme.gutter, paddingTop: tv ? 24 : 10}}>
      <AppliedFilterChips platform={target} filters={applied} onRemove={id => onRemoveFilter!(id)} onClear={onClearFilters}>{tv && count}</AppliedFilterChips>
    </View>}
  </View>;
}
