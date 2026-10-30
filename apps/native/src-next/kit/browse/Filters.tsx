import React, {useRef, useState, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import MaskedView from '@react-native-masked-view/masked-view';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Chip, Segmented, TextField, Focusable} from '../controls';
import {Icon} from '../Icon';
import {Popover} from '../overlays';
import {focusTestID} from '../focus';
import {useTheme} from '../theme';
import {alpha, browsePlatform, browseType, changeFacetSelection, nextFacetState} from './presentation';
import type {BrowsePlatform, FacetValue, FilterBuilderProps, FilterFieldState, FilterFieldValue, MatchModeValue} from './types';
import type {BrowseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';

const t = defaultI18n.t;
const groupLabels = {title: 'next.browse.native.aboutTitle', activity: 'next.browse.native.activity', file: 'next.browse.native.aboutFile'} as const;
const focusProps = (prefix: string | undefined, ...identity: string[]) => prefix === undefined ? {} : {
  focusId: JSON.stringify([prefix, ...identity]), testID: focusTestID(prefix, JSON.stringify(identity)),
};

export function MatchMode({value, allowAll, onValueChange, focusPrefix}: {value: MatchModeValue; allowAll: boolean; onValueChange: (value: MatchModeValue) => void; focusPrefix?: string}) {
  return <Segmented variant="browse" value={value} accessibilityLabel={t('next.browse.native.match')} onValueChange={next => onValueChange(next as MatchModeValue)}
    itemProps={focusPrefix === undefined ? undefined : item => focusProps(focusPrefix, 'match', item.value)} items={[
    {value: 'any', label: t('next.browse.native.anyOf')},
    ...(allowAll ? [{value: 'all', label: t('next.browse.native.allOf')}] : []),
    {value: 'none', label: t('next.browse.native.noneOf')},
  ]}/>;
}

export function FacetChips({values, included, excluded, onValueChange, cycleExclusion = false, platform, trailing, preferredFocusValue, focusPrefix}: {
  values: readonly FacetValue[]; included: readonly string[]; excluded: readonly string[];
  onValueChange: (value: string, state: 'include' | 'exclude' | 'clear') => void;
  cycleExclusion?: boolean; platform?: BrowsePlatform; trailing?: ReactNode; preferredFocusValue?: string; focusPrefix?: string;
}) {
  const theme = useTheme(), target = browsePlatform(theme, platform), tv = target === 'tv';
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: tv ? 12 : 8}}>
    {values.map(item => {
      const include = included.includes(item.value), exclude = excluded.includes(item.value);
      return <Focusable key={item.value} {...focusProps(focusPrefix, 'facet', item.value)} focusKind="chip" focusRadius={tv ? 12 : 17} hasTVPreferredFocus={tv && preferredFocusValue === item.value} disabled={item.disabled} accessibilityRole="checkbox" accessibilityState={{checked: exclude ? 'mixed' : include, disabled: item.disabled}} onPress={() => onValueChange(item.value, nextFacetState(item.value, included, excluded, cycleExclusion))}
        style={({focused}) => ({height: tv ? 56 : 34, justifyContent: 'center', borderRadius: tv ? 12 : 17, paddingHorizontal: tv ? 20 : 12, borderColor: exclude ? alpha(theme.colors.record, .45) : include ? tv ? 'rgba(92,179,226,.7)' : theme.colors.accent : tv ? 'transparent' : theme.colors.lineStrong, borderWidth: tv ? 1.5 : 1, backgroundColor: tv && focused ? alpha(theme.colors.text, .16) : include ? alpha(theme.colors.accent, tv ? .24 : .28) : tv ? alpha(theme.colors.text, .08) : 'transparent'})}>
        {({focused}) => <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 8 : 6}}>
          {include && <Icon name="i-check" size={tv ? 22 : 14} color={theme.colors.accentStrong}/>}
          <Text numberOfLines={1} style={[browseType(theme, tv ? 22 : 13.5, tv ? 700 : 600), {color: tv && focused ? theme.colors.text : exclude ? theme.colors.record : include ? theme.colors.text : theme.colors.textSecondary, textDecorationLine: exclude ? 'line-through' : 'none'}]}>{item.label}</Text>
          {item.count !== undefined && <Text style={[browseType(theme, tv ? 19 : 12, 600), {color: include ? theme.colors.textSecondary : theme.colors.textTertiary, fontVariant: ['tabular-nums'], textDecorationLine: exclude ? 'line-through' : 'none'}]}>{defaultI18n.number(item.count)}{item.countIsLowerBound ? t('next.browse.native.countPlus') : ''}</Text>}
        </View>}
      </Focusable>;
    })}
    {trailing}
  </View>;
}

export function FieldTile({label, summary, active = false, open = false, onOpenChange, platform, children, more = false, focusPrefix}: {
  label: ReactNode; summary?: ReactNode; active?: boolean; open?: boolean; onOpenChange: (open: boolean) => void; platform?: BrowsePlatform; children?: ReactNode; more?: boolean; focusPrefix?: string;
}) {
  const theme = useTheme(), target = browsePlatform(theme, platform), tv = target === 'tv', desktop = target === 'desktop';
  return <View style={{minWidth: 0, borderRadius: desktop ? 12 : 14, backgroundColor: open ? tv ? alpha(theme.colors.text, .05) : theme.colors.raisedHigh : desktop ? theme.colors.raisedHigh : 'transparent', borderWidth: desktop && active ? 1 : 0, borderColor: alpha(theme.colors.accentStrong, .5), overflow: 'visible'}}>
    <Focusable {...focusProps(focusPrefix, 'open')} accessibilityRole="button" focusKind="row" focusRadius={14} onPress={() => onOpenChange(!open)} accessibilityState={{expanded: open}} style={{height: tv ? 64 : desktop ? 58 : 48, paddingHorizontal: tv ? 20 : 14, width: '100%', justifyContent: 'center'}}>
      <View style={{flex: 1, minWidth: 0, flexDirection: desktop && !open ? 'column' : 'row', alignItems: desktop && !open ? 'flex-start' : 'center', gap: desktop && !open ? 2 : 10, paddingRight: desktop && !open ? 26 : 0}}>
        <Text numberOfLines={1} style={[browseType(theme, tv ? 24 : desktop && !open ? 12.5 : 15, 600), {color: desktop && !open ? theme.colors.textTertiary : theme.colors.text, flexShrink: 1}]}>{label}</Text>
        <Text numberOfLines={1} style={[browseType(theme, tv ? 24 : desktop && !open ? 15 : 14, active ? 700 : tv ? 500 : 600), {color: active ? theme.colors.accentStrong : theme.colors.textTertiary, marginLeft: desktop && !open ? 0 : 'auto', flexShrink: 1}]}>{summary ?? t('next.browse.native.any')}</Text>
        <Icon name={more ? 'i-plus' : open ? tv ? 'i-down' : 'i-up' : 'i-right'} size={tv ? 24 : 16} color={active ? theme.colors.accentStrong : theme.colors.textTertiary} style={desktop && !open ? {position: 'absolute', right: 0, top: 5} : undefined}/>
      </View>
    </Focusable>
    {open && children && <View style={{paddingHorizontal: tv ? 20 : desktop ? 16 : 14, paddingTop: tv ? 6 : undefined, paddingBottom: tv ? 22 : 14, gap: tv ? 14 : 12}}>{children}</View>}
  </View>;
}

function FieldControl({id, label, state, platform, onChange, onSearch, onShowAll, onRetry, focusPrefix}: {
  id: string; label: string; state: FilterFieldState; platform: BrowsePlatform;
  onChange: (value: FilterFieldValue) => void; onSearch: (query: string) => void; onShowAll?: () => void; onRetry?: () => void; focusPrefix?: string;
}) {
  const theme = useTheme();
  const help = state.help ?? (id === 'year' ? t('next.browse.native.decadesHelp') : id === 'genre' && platform === 'phone' ? t('next.browse.native.includeHelp') : undefined);
  const status = state.status === 'loading' || state.status === 'error';
  return <>
    {state.kind === 'values' && <>
      {state.showMatchMode && <MatchMode focusPrefix={focusPrefix} value={state.mode} allowAll={state.allowAll} onValueChange={mode => onChange({kind: 'values', mode, included: state.included, excluded: state.excluded})}/>}
      {state.search !== undefined && platform !== 'tv' && <TextField {...focusProps(focusPrefix, 'search')} variant="search" leadingIcon="i-search" accessibilityLabel={state.searchLabel ?? t('next.browse.native.find', {field: label.toLocaleLowerCase('en-US')})} placeholder={state.searchLabel ?? t('next.browse.native.find', {field: label.toLocaleLowerCase('en-US')})} value={state.search} onChangeText={onSearch} fieldStyle={{height: platform === 'phone' ? 38 : 40, minHeight: platform === 'phone' ? 38 : 40}}/>}
      {id === 'year' && help && <Text style={[browseType(theme, platform === 'tv' ? 20 : 12.5, 500), {color: theme.colors.textTertiary}]}>{help}</Text>}
      {(!status || state.values.length > 0) && <FacetChips focusPrefix={focusPrefix} platform={platform} values={state.values} included={state.included} excluded={state.excluded} preferredFocusValue={state.preferredFocusValue} cycleExclusion={state.cycleExclusion ?? platform === 'phone'} onValueChange={(value, choice) => onChange({kind: 'values', mode: state.mode, ...changeFacetSelection(value, choice, state.included, state.excluded)})}
        trailing={onShowAll && state.showAllLabel ? <Button {...focusProps(focusPrefix, 'show-all')} variant="text" size="small" onPress={onShowAll}>{state.showAllLabel}</Button> : undefined}/>}
    </>}
    {state.kind === 'choice' && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{state.options.map(option => <Chip key={option.value} {...focusProps(focusPrefix, 'choice', option.value)} variant="facet" selected={state.value === option.value} disabled={option.disabled} onPress={() => onChange({kind: 'choice', value: state.value === option.value ? null : option.value})} accessibilityRole="checkbox" accessibilityState={{checked: state.value === option.value}}>{option.label}</Chip>)}</View>}
    {state.kind === 'toggle' && <Segmented variant="phone" value={state.value === null ? 'any' : state.value ? 'yes' : 'no'} onValueChange={value => onChange({kind: 'toggle', value: value === 'any' ? null : value === 'yes'})}
      itemProps={focusPrefix === undefined ? undefined : item => focusProps(focusPrefix, 'toggle', item.value)} items={[{value: 'any', label: t('next.browse.native.any')}, {value: 'yes', label: t('next.kit.yes')}, {value: 'no', label: t('next.kit.no')}]}/>}
    {state.kind === 'range' && <View style={{gap: 8, flexDirection: 'row', flexWrap: 'wrap'}}>{state.presets.map(preset => <Chip key={preset.value} {...focusProps(focusPrefix, 'range', preset.value)} variant="facet" selected={state.value === preset.value} onPress={() => onChange({kind: 'range', value: preset.value === state.value ? null : preset.value})}>{preset.label}</Chip>)}</View>}
    {state.kind === 'text' && <TextField {...focusProps(focusPrefix, 'text')} value={state.value} accessibilityLabel={state.inputLabel ?? label} onChangeText={value => onChange({kind: 'text', value})}/>}
    {status && <View accessibilityLiveRegion="polite" style={{gap: 8}}>{state.statusLabel && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{state.statusLabel}</Text>}{state.status === 'error' && onRetry && <Button {...focusProps(focusPrefix, 'retry')} variant="text" size="small" onPress={onRetry}>{t('action.tryAgain')}</Button>}</View>}
    {id !== 'year' && help && <Text style={[browseType(theme, platform === 'tv' ? 20 : 12.5, 500), {color: theme.colors.textTertiary}]}>{help}</Text>}
  </>;
}

/** Content only. SidePanel/BottomSheet supplies title, dismissal, focus, footer and detents. */
export function FilterBuilder({model, labels, platform, focusPrefix, openFields, onFieldOpenChange, fields, onFieldChange, onSearchChange, onShowAll, onRetry, more, children, testID}: FilterBuilderProps) {
  const theme = useTheme(), target = browsePlatform(theme, platform), tv = target === 'tv', desktop = target === 'desktop';
  const renderField = (field: BrowseToolbarModel['groups'][number]['fields'][number]) => {
    const state = fields[field.id];
    if (!state) throw new Error(`Missing controlled browse field: ${field.id}`);
    const open = openFields.includes(field.id), name = labels.field(field);
    const fieldPrefix = focusPrefix === undefined ? undefined : JSON.stringify([focusPrefix, 'field', field.id]);
    const summary = state.summary ?? (state.kind === 'values' && state.included.length + state.excluded.length > 0 ? t('next.browse.native.chosen', {count: state.included.length + state.excluded.length}) : undefined);
    const control = <FieldControl id={field.id} focusPrefix={fieldPrefix} label={name} state={state} platform={target} onChange={value => onFieldChange(field.id, value)} onSearch={query => onSearchChange(field.id, query)} onShowAll={onShowAll ? () => onShowAll(field.id) : undefined} onRetry={onRetry ? () => onRetry(field.id) : undefined}/>;
    if (state.kind === 'toggle' && !desktop) return <View key={field.id} style={{minHeight: tv ? 64 : 48, flexDirection: 'row', alignItems: 'center', paddingHorizontal: tv ? 20 : 14, gap: 8, borderTopWidth: tv ? 0 : 1, borderTopColor: alpha(theme.colors.lineStrong, .5)}}>
      <Text style={[browseType(theme, tv ? 24 : 15), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text><View style={{marginLeft: 'auto'}}>{control}</View>
    </View>;
    // H10: on large screens a field opens in a popover anchored to its tile, so the two-column grid never reflows.
    if (desktop) return <DesktopField key={field.id} label={name} open={open} onOpenChange={next => onFieldOpenChange(field.id, next)}
      tile={<FieldTile focusPrefix={fieldPrefix} label={name} summary={summary} active={state.active} open={false} onOpenChange={() => onFieldOpenChange(field.id, !open)} platform={target}/>}>{control}</DesktopField>;
    return <View key={field.id} style={{width: desktop && !open ? '48.8%' : '100%', borderTopWidth: !desktop && !tv ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .5)}}><FieldTile focusPrefix={fieldPrefix} label={name} summary={summary} active={state.active} open={open} onOpenChange={next => onFieldOpenChange(field.id, next)} platform={target}>{control}</FieldTile></View>;
  };
  const content = <ScrollView testID={testID} style={{flex: 1}} contentContainerStyle={{paddingBottom: tv ? 120 : 20}} showsVerticalScrollIndicator={false} keyboardShouldPersistTaps="handled">
    {model.groups.map((group, index) => {
      const rare = more?.[group.id], primary = group.fields.filter(field => !rare?.fields.includes(field.id)), secondary = group.fields.filter(field => rare?.fields.includes(field.id));
      return <View key={group.id} style={{marginTop: tv ? index === 0 ? 0 : 22 : 14}}>
        <Text accessibilityRole="header" style={[browseType(theme, tv ? 20 : desktop ? 15 : 14, 700), {color: tv || !desktop ? theme.colors.textTertiary : theme.colors.text, marginBottom: tv ? 8 : 8, marginHorizontal: tv ? 20 : desktop ? 0 : 20}]}>{t(groupLabels[group.id])}</Text>
        <View style={{marginHorizontal: desktop || tv ? 0 : 16, borderRadius: 14, backgroundColor: !desktop && !tv ? theme.colors.raisedHigh : undefined, flexDirection: desktop ? 'row' : 'column', flexWrap: desktop ? 'wrap' : undefined, gap: desktop ? 10 : 0}}>
          {primary.map(renderField)}
          {rare && secondary.length > 0 && <View style={{width: '100%'}}><FieldTile focusPrefix={focusPrefix === undefined ? undefined : JSON.stringify([focusPrefix, 'more', group.id])} label={t('next.browse.native.more')} summary={rare.summary} open={rare.open} onOpenChange={rare.onOpenChange} platform={target} more>{secondary.map(renderField)}</FieldTile></View>}
        </View>
      </View>;
    })}
    {children}
  </ScrollView>;
  return tv ? <TVFilterFade>{content}</TVFilterFade> : content;
}

function DesktopField({label, open, onOpenChange, tile, children}: {label: string; open: boolean; onOpenChange: (open: boolean) => void; tile: ReactNode; children: ReactNode}) {
  const anchor = useRef<View>(null);
  return <View ref={anchor} collapsable={false} style={{width: '48.8%'}}>
    {tile}
    <Popover open={open} onOpenChange={next => onOpenChange(next)} anchorRef={anchor} title={label} showTitle placement="bottom-start" width={340}>{children}</Popover>
  </View>;
}

function TVFilterFade({children}: {children: ReactNode}) {
  const [height, setHeight] = useState(0);
  return <MaskedView style={{flex: 1}} androidRenderingMode="hardware" onLayout={event => setHeight(event.nativeEvent.layout.height)} maskElement={
    <Svg width="100%" height="100%"><Defs><LinearGradient id="filter-bottom-fade" x1="0" y1="0" x2="0" y2="100%">
      <Stop offset={height > 0 ? Math.max(0, 1 - 120 / height) : 1} stopColor="#fff"/>
      <Stop offset="1" stopColor="#fff" stopOpacity={0}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#filter-bottom-fade)"/></Svg>
  }>{children}</MaskedView>;
}
