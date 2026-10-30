import React, {useRef, useState, type ReactNode} from 'react';
import {StyleSheet, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Focusable} from '../controls/Focusable';
import {Select, type SelectOption} from '../controls/Select';
import {Menu} from '../overlays/Menu';
import {Artwork} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID} from '../focus/registry';
import {HighlightedText} from './HighlightedText';
import {alpha, shellType} from './visual';

export type SettingsRowProps = {
  testID?: string;
  label: string;
  highlightQuery?: string;
  description?: string;
  scope?: string;
  control?: ReactNode;
  value?: string;
  onPress?: () => void;
  readable?: boolean;
  /** TV switch row: the row itself is the focus stop and OK toggles; `visual` is drawn only. */
  toggle?: Readonly<{checked: boolean; visual: ReactNode}>;
  dependent?: boolean;
  continuesBranch?: boolean;
  highlighted?: boolean;
  /** A consequential action's row (sign out): its label reads in the record colour. */
  destructive?: boolean;
  first?: boolean;
  style?: StyleProp<ViewStyle>;
  nextFocusLeft?: number;
};
/** A slotted control keeps its sole focus target. A TV readable row intentionally has no OK action. */
export function SettingsRow({label, highlightQuery, description, scope, control, value, onPress, readable = false, toggle, dependent = false, continuesBranch = false, highlighted = false, destructive = false, first = false, style, nextFocusLeft, testID}: SettingsRowProps) {
  const theme = useTheme();
  const tv = theme.isTV;
  const content = <>
    {dependent && <View pointerEvents="none" style={{position: 'absolute', left: tv ? 38 : 31, top: -1, height: '50%', width: 14, borderLeftWidth: 2, borderBottomWidth: 2, borderBottomLeftRadius: 9, borderColor: theme.colors.line}}/>}
    {dependent && continuesBranch && <View pointerEvents="none" style={{position: 'absolute', left: tv ? 38 : 31, top: 0, bottom: 0, width: 2, backgroundColor: theme.colors.line}}/>}
    {highlighted && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 14, bottom: 14, width: 3, borderRadius: 3, backgroundColor: theme.colors.accent}}/>}
    <View style={{flex: 1, minWidth: 0}}><HighlightedText text={label} query={highlightQuery} style={[shellType(tv ? 24 : 15.5), {color: destructive ? theme.colors.record : theme.colors.text}]}/>
      {description && <Text numberOfLines={tv ? 2 : undefined} style={[shellType(tv ? 20 : 12.5, tv ? 400 : 500, tv ? 29 : 17.5), {color: theme.colors.textTertiary, marginTop: 3}]}>{description}</Text>}
      {scope && <Text style={[shellType(tv ? 18 : 12, 600), {color: theme.colors.textTertiary, marginTop: 3}]}>{scope}</Text>}
    </View>
    {control ?? (toggle ? <View pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants">{toggle.visual}</View> : value && <Text style={[shellType(tv ? 22 : 14.5), {color: destructive ? theme.colors.record : theme.colors.textSecondary, maxWidth: tv ? 340 : 170}]}>{value}</Text>)}
    {onPress && !control && !toggle && <Icon name="i-updown" size={tv ? 26 : 18} color={destructive ? theme.colors.record : theme.colors.textTertiary}/>}
  </>;
  const layout: StyleProp<ViewStyle> = [{flexDirection: 'row', alignItems: 'center', gap: tv ? 20 : 12, paddingHorizontal: tv ? 24 : 14, paddingVertical: tv ? 12 : 8, paddingLeft: dependent ? tv ? 64 : 58 : tv ? 24 : 14, minHeight: tv ? 80 : 48, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft, borderRadius: tv ? 18 : 0, backgroundColor: highlighted ? alpha(theme.colors.accent, .16) : 'transparent'}, style];
  // TV: a value-only row is a readable stop (OK does nothing) so every line can be reached and read.
  const readableStop = tv && !onPress && (readable || toggle !== undefined || value !== undefined);
  return (onPress || readableStop) && !control ? <Focusable testID={testID} accessibilityRole={toggle ? 'switch' : onPress ? 'button' : 'text'} accessibilityLabel={label} accessibilityHint={description} accessibilityValue={value ? {text: value} : undefined} accessibilityState={toggle ? {checked: toggle.checked} : undefined} onPress={onPress} focusKind="row" focusRadius={tv ? 18 : 10} nextFocusLeft={nextFocusLeft} style={layout}>{content}</Focusable> : <View style={layout}>{content}</View>;
}
export function DefaultHint({value, onReset, changed, testID}: {testID?: string; value: string; onReset: () => void; changed: boolean}) {
  const theme = useTheme();
  if (!changed) return null;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 6, flexWrap: 'wrap'}}><Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{defaultI18n.t('next.shell.defaultHint', {value})}</Text><Focusable testID={controlID(testID, 'reset')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.shell.reset')} onPress={onReset} hitSlop={12}><Text style={[shellType(13, 700), {color: theme.colors.accentStrong}]}>{defaultI18n.t('next.shell.reset')}</Text></Focusable></View>;
}
export type ChoiceMatrixRow = Readonly<{id: string; label: string; value: string; defaultHint?: ReactNode}>;
export function ChoiceMatrix({rows, options, onChange, testID}: {testID?: string; rows: readonly ChoiceMatrixRow[]; options: readonly SelectOption[]; onChange: (id: string, value: string) => void}) {
  const theme = useTheme();
  if (theme.platform === 'phone' || theme.isTV) return <View>{rows.map((row, index) => <SettingsRow key={row.id} label={row.label} first={index === 0} control={<Select testID={controlID(testID, 'row', row.id)} accessibilityLabel={row.label} value={row.value} options={options} onValueChange={value => onChange(row.id, value)} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/>)}</View>;
  return <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
    <View style={{flexDirection: 'row', paddingTop: 18, paddingBottom: 8}}><View style={{flex: 1}}/>{options.map(option => <Text key={option.value} style={[shellType(14, 700), {color: theme.colors.textSecondary, width: 104, textAlign: 'center'}]}>{option.label}</Text>)}</View>
    {rows.map(row => <View key={row.id} accessibilityRole="radiogroup" accessibilityLabel={row.label} style={{flexDirection: 'row', minHeight: 52, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
      <View style={{flex: 1, paddingVertical: 16, paddingHorizontal: 22}}><Text style={[shellType(16), {color: theme.colors.text}]}>{row.label}</Text>{row.defaultHint}</View>
      {options.map(option => <Focusable key={option.value} testID={controlID(testID, 'row', row.id, 'option', option.value)} accessibilityRole="radio" accessibilityLabel={typeof option.label === 'string' ? option.label : undefined} accessibilityState={{checked: option.value === row.value}} disabled={option.disabled} onPress={() => onChange(row.id, option.value)} focusRadius={0} style={{width: 104, alignItems: 'center', justifyContent: 'center'}}><View style={{width: 22, height: 22, borderRadius: 11, borderWidth: 2, borderColor: option.value === row.value ? theme.colors.accentStrong : theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}>{option.value === row.value && <View style={{width: 9.2, height: 9.2, borderRadius: 5, backgroundColor: theme.colors.text}}/>}</View></Focusable>)}
    </View>)}
  </View>;
}
export function ChoiceCards({label, value, options, onChange, testID}: {testID?: string; label: string; value: string; options: readonly SelectOption[]; onChange: (value: string) => void}) {
  const theme = useTheme();
  return <View accessibilityRole="radiogroup" accessibilityLabel={label} style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{options.map((option, index) => <Focusable key={option.value} testID={controlID(testID, 'option', option.value)} accessibilityRole="radio" accessibilityLabel={typeof option.label === 'string' ? option.label : undefined} accessibilityHint={typeof option.description === 'string' ? option.description : undefined} accessibilityState={{checked: option.value === value}} disabled={option.disabled} onPress={() => onChange(option.value)} focusRadius={0} style={{flexDirection: 'row', gap: 16, paddingVertical: 17, paddingHorizontal: 22, borderTopWidth: index ? 1 : 0, borderTopColor: theme.colors.lineSoft, backgroundColor: option.value === value ? alpha(theme.colors.accent, .13) : 'transparent'}}>
    {option.value === value && <View style={{position: 'absolute', left: 0, top: 14, bottom: 14, width: 3, borderRadius: 3, backgroundColor: theme.colors.accent}}/>}
    <View style={{width: 22, height: 22, marginTop: 1, borderRadius: 11, borderWidth: 2, borderColor: option.value === value ? theme.colors.accentStrong : theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}>{option.value === value && <View style={{width: 9.2, height: 9.2, borderRadius: 5, backgroundColor: theme.colors.text}}/>}</View>
    <View style={{flex: 1}}><Text style={[shellType(16), {color: theme.colors.text}]}>{option.label}</Text>{option.description && <Text style={[shellType(14, 400, 20.3), {marginTop: 4, color: theme.colors.textTertiary}]}>{option.description}</Text>}</View>
  </Focusable>)}</View>;
}

export type LanguageOption = Readonly<{id: string; label: string}>;
export function LanguageList({label, value, available, limit, onChange, testID, initialFocusRef}: {testID?: string; /** Receives the first language row (or Add language when the list is empty) so a sheet can open on it. */ initialFocusRef?: {current: View | null}; label: string; value: readonly LanguageOption[]; available: readonly LanguageOption[]; limit: number; onChange: (value: readonly LanguageOption[]) => void}) {
  const theme = useTheme();
  const [menu, setMenu] = useState<LanguageOption | null>(null);
  const [adding, setAdding] = useState(false);
  const languageRefs = useRef(new Map<string, View>()), languageReturn = useRef<View | null>(null), addRef = useRef<View>(null);
  const lastLanguage = useRef<string | undefined>(undefined);
  const move = (id: string, offset: number) => {
    const current = value.findIndex(language => language.id === id), next = current + offset;
    if (current < 0 || next < 0 || next >= value.length) return;
    const changed = [...value]; [changed[current], changed[next]] = [changed[next], changed[current]]; onChange(changed);
  };
  const remaining = available.filter(option => !value.some(item => item.id === option.id));
  return <View accessibilityLabel={label} style={{gap: 8}}>
    {!value.length && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{defaultI18n.t('next.shell.native.noLanguages')}</Text>}
    {value.map((language, index) => <View key={language.id} style={{flexDirection: 'row', alignItems: 'center', gap: 10, backgroundColor: theme.colors.raisedBright, borderRadius: 10, paddingHorizontal: 8, minHeight: theme.isTV ? 64 : 40}}>
      <Text style={[shellType(theme.isTV ? 20 : 12, 700), {color: theme.colors.textSecondary, backgroundColor: alpha(theme.colors.ink, .55), padding: 4, borderRadius: 6}]}>{defaultI18n.number(index + 1)}</Text>
      <Focusable testID={controlID(testID, 'language', language.id)} ref={node => {if (node) languageRefs.current.set(language.id, node); else languageRefs.current.delete(language.id); if (index === 0 && initialFocusRef) initialFocusRef.current = node;}} accessibilityRole="button" accessibilityLabel={language.label} onPress={() => {languageReturn.current = languageRefs.current.get(language.id) ?? null; lastLanguage.current = language.id; setMenu(language);}} focusKind="row" style={{flex: 1}}><Text style={[shellType(theme.isTV ? 22 : 15, 700), {color: theme.colors.text}]}>{language.label}</Text></Focusable>
      {!theme.isTV && <>
        {index > 0 && <Focusable testID={controlID(testID, 'language', language.id, 'move-up')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('settings.languages.moveUp', {language: language.label})} onPress={() => move(language.id, -1)} hitSlop={8}><Icon name="i-up" size={18} color={theme.colors.textTertiary}/></Focusable>}
        {index + 1 < value.length && <Focusable testID={controlID(testID, 'language', language.id, 'move-down')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('settings.languages.moveDown', {language: language.label})} onPress={() => move(language.id, 1)} hitSlop={8}><Icon name="i-down" size={18} color={theme.colors.textTertiary}/></Focusable>}
        <Focusable testID={controlID(testID, 'language', language.id, 'remove')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('settings.languages.remove', {language: language.label})} onPress={() => onChange(value.filter(item => item.id !== language.id))} hitSlop={8}><Icon name="i-x" size={18} color={theme.colors.textTertiary}/></Focusable>
      </>}
    </View>)}
    {value.length < limit && remaining.length > 0 && <Focusable testID={controlID(testID, 'add')} ref={node => {addRef.current = node; if (!value.length && initialFocusRef) initialFocusRef.current = node;}} accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.shell.addLanguage')} onPress={() => setAdding(true)} style={{flexDirection: 'row', alignItems: 'center', gap: 6, minHeight: theme.isTV ? 64 : 40, paddingHorizontal: 10, borderRadius: 10, borderWidth: 1, borderColor: theme.colors.lineStrong}}><Icon name="i-plus" size={20}/><Text style={[shellType(theme.isTV ? 22 : 14, 700), {color: theme.colors.textSecondary}]}>{defaultI18n.t('next.shell.addLanguage')}</Text></Focusable>}
    <Menu testID={controlID(testID, 'language-menu', menu?.id ?? lastLanguage.current ?? '')} returnFocusRef={languageReturn} open={Boolean(menu)} onOpenChange={open => {if (!open) setMenu(null);}} title={menu?.label ?? label} presentation={theme.isTV ? 'sideSheet' : 'sheet'} items={menu ? [
      ...(value[0]?.id !== menu.id ? [{id: 'up', label: defaultI18n.t('settings.languages.moveUp', {language: menu.label}), onSelect: () => move(menu.id, -1)}] : []),
      ...(value[value.length - 1]?.id !== menu.id ? [{id: 'down', label: defaultI18n.t('settings.languages.moveDown', {language: menu.label}), onSelect: () => move(menu.id, 1)}] : []),
      {id: 'remove', label: defaultI18n.t('settings.languages.remove', {language: menu.label}), onSelect: () => onChange(value.filter(item => item.id !== menu.id))},
    ] : []}/>
    <Menu testID={controlID(testID, 'add-menu')} returnFocusRef={addRef} open={adding} onOpenChange={setAdding} title={defaultI18n.t('next.shell.addLanguage')} presentation={theme.isTV ? 'sideSheet' : 'sheet'} items={remaining.map(option => ({id: option.id, label: option.label, onSelect: () => {if (value.length < limit) onChange([...value, option]);}}))}/>
  </View>;
}

export function SubtitlePreview({source, size, background, visible = true}: {source: ArtworkSource; size: 'small' | 'medium' | 'large' | 'extra-large'; background: 'none' | 'translucent' | 'opaque'; visible?: boolean}) {
  const theme = useTheme();
  const fontSize = ({small: 20, medium: 26, large: 32, 'extra-large': 40} as const)[size] * (theme.isTV ? 30 / 26 : 1);
  return <View style={{height: theme.isTV ? 190 : 360, borderRadius: theme.isTV ? 18 : 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
    <Artwork source={source} visible={visible} position={{x: .5, y: .3}}/>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.ink, theme.isTV ? .3 : .1)}]}/>
    <View style={{position: 'absolute', bottom: theme.isTV ? 26 : 70, left: 0, right: 0, alignItems: 'center'}}><Text style={[shellType(fontSize, 600, fontSize * 1.3, 0, 1), {color: theme.colors.white, paddingHorizontal: theme.isTV ? 18 : 12, paddingVertical: theme.isTV ? 6 : 3, borderRadius: theme.isTV ? 6 : 4, backgroundColor: background === 'none' ? 'transparent' : alpha(theme.colors.shadow, background === 'opaque' ? 1 : .55), textShadowColor: background === 'opaque' ? 'transparent' : alpha(theme.colors.shadow, .8), textShadowOffset: {width: 0, height: 1}, textShadowRadius: 3}]}>{defaultI18n.t('settings.subtitlePreview.line')}</Text></View>
  </View>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
