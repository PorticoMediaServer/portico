import React, {useEffect, useId, useRef} from 'react';
import {BackHandler, StyleSheet, View} from 'react-native';
import Svg, {Defs, LinearGradient, RadialGradient, Rect, Stop} from 'react-native-svg';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {Menu, MenuRow} from '../overlays/Menu';
import type {FocusRef, MenuEntry} from '../overlays/types';
import {useTheme} from '../theme';
import {ServerMark} from '../shell/ServerIdentity';
import {selectionText} from './copy';
import {selectionControlID as controlID} from './focus-ids';
import {SelectionSurface, SelectionText} from './surface';
import {SelectionActionRow} from './SelectionActionRow';
import type {SelectionAction, SelectionActionOffer, SelectionBarProps} from './types';

const icons: Record<SelectionAction, IconName> = {play: 'i-play', queue: 'i-queue', playlist: 'i-listplus', myList: 'i-plus', watched: 'i-check', unwatched: 'i-eyeoff', collection: 'i-layers', favorite: 'i-heart', removeMyList: 'i-x', download: 'i-download', edit: 'i-edit', refresh: 'i-refresh', delete: 'i-trash', playNext: 'i-queue', playLater: 'i-listadd', more: 'i-more'};
const ownerActions: readonly SelectionAction[] = ['edit', 'refresh', 'delete'];
const mainTV: readonly SelectionAction[] = ['play', 'queue', 'playlist', 'myList', 'watched', 'more'];
const phoneMain: readonly SelectionAction[] = ['play', 'queue', 'playlist', 'myList', 'more'];
const label = (id: SelectionAction) => selectionText(`next.selection.native.action.${id}`);

// Admission belongs to the canonical model/server offers, including TV curation.
// select 24: a TV selection has no owner area, so owner editing and deletion are
// never drawn there even when a catalogue offer would allow them. Only disabled
// whole-query Delete/Download explanations may pass; they have no executor.
const tvHidden: ReadonlySet<SelectionAction> = new Set<SelectionAction>(['download', 'edit', 'refresh', 'delete']);
function permitted(actions: readonly SelectionActionOffer[], isTV: boolean, largeView: boolean) {return actions.filter(action => !isTV || !tvHidden.has(action.id)
  || largeView && action.wholeQueryUnavailable === true && action.disabled === true && !!action.reason && (action.id === 'delete' || action.id === 'download'));}
function SelectionMenus(props: SelectionBarProps & {returnFocusRef?: FocusRef}) {
  const {isTV, colors, platform} = useTheme();
  const queueFirst = useRef<View>(null);
  const ownerGradient = useId().replace(/:/g, '');
  const retainedMenu = useRef(props.menu ?? 'more'), retainedReturn = useRef(props.returnFocusRef);
  if (props.menu !== null) {retainedMenu.current = props.menu; retainedReturn.current = props.returnFocusRef;}
  const menu = props.menu ?? retainedMenu.current;
  const actions = permitted(props.actions, isTV, props.count > 200);
  const menuIds: readonly SelectionAction[] = menu === 'queue' ? ['playNext', 'playLater'] : menu === 'watched' ? ['watched', 'unwatched'] : [...(props.fourthSlot === 'watched' ? ['myList' as const] : []), 'watched', 'unwatched', 'collection', 'favorite', 'download', 'removeMyList', 'edit', 'refresh', 'delete'];
  const items: MenuEntry[] = [];
  for (const id of menuIds) {
    const offer = actions.find(action => action.id === id);
    if (!offer || isTV && menu === 'more' && id === 'watched') continue;
    if (ownerActions.includes(id) && !items.some(item => 'section' in item)) items.push({id: 'owner', section: true, label: props.ownerTag, tone: 'owner'});
    items.push({id, label: id === 'watched' ? selectionText('next.selection.native.markWatched') : label(id), icon: icons[id], description: offer.reason ?? offer.description, disabled: offer.disabled,
      tone: ownerActions.includes(id) ? 'owner' : 'default', destructive: id === 'delete', onSelect: () => {if (!offer.disabled) props.onAction(id);}});
  }
  const actionItems = items.filter((entry): entry is Extract<MenuEntry, {onSelect: unknown}> => 'onSelect' in entry);
  if (!isTV && (menu === 'queue' || menu === 'more')) {
    const first = actionItems.find(entry => !entry.disabled);
    const row = (entry: typeof actionItems[number], owner = false) => <SelectionActionRow key={entry.id} testID={`${controlID(props.testID, 'menu', menu)}-item-${entry.id}`} ref={entry === first ? queueFirst : undefined}
      label={entry.label} icon={entry.icon} disabled={entry.disabled} destructive={entry.destructive} tone={entry.id === 'edit' ? 'owner' : undefined}
      description={entry.description ?? (menu === 'queue' ? selectionText(entry.id === 'playNext' ? 'next.selection.native.queueNextDescription' : 'next.selection.native.queueLaterDescription') : entry.id === 'edit' ? defaultI18n.t('next.selection.moreEditDescription', {items: props.selectionLabel ?? selectionText('next.selection.native.titles', {count: props.count})}) : undefined)}
      style={owner ? {paddingHorizontal: 14, paddingVertical: 6} : undefined} onPress={() => {if (!entry.disabled) {entry.onSelect(); props.onMenuChange(null);}}} />;
    const owners = actionItems.filter(entry => ownerActions.includes(entry.id as SelectionAction));
    return <SelectionSurface compactHeader testID={controlID(props.testID, 'menu', menu)} open={props.menu !== null} onOpenChange={open => {if (!open) props.onMenuChange(null);}}
      title={menu === 'queue' ? selectionText('next.selection.native.queueTitle', {items: props.selectionLabel ?? selectionText('next.selection.native.titles', {count: props.count})}) : props.menuTitle ?? defaultI18n.t('next.selection.selectedItems', {items: props.selectionLabel ?? selectionText('next.selection.native.titles', {count: props.count})})}
      description={menu === 'queue' ? selectionText('next.selection.native.queueOrder') : undefined} initialFocusRef={queueFirst} returnFocusRef={props.returnFocusRef ?? retainedReturn.current}>
      {actionItems.filter(entry => !ownerActions.includes(entry.id as SelectionAction)).map(entry => row(entry))}
      {owners.length ? <View style={{marginHorizontal: 12, marginTop: 8, paddingVertical: 4, borderRadius: 16, backgroundColor: '#0b1822', borderWidth: platform === 'phone' ? 0 : 1, borderColor: 'rgba(92,179,226,.18)', overflow: 'hidden'}}>
        <Svg pointerEvents="none" style={StyleSheet.absoluteFill} width="100%" height="100%"><Defs><RadialGradient id={ownerGradient} cx="100%" cy="0%" rx="120%" ry="100%"><Stop offset="0" stopColor={colors.accentDeep} stopOpacity={.24} /><Stop offset={.62} stopColor={colors.accentDeep} stopOpacity={0} /></RadialGradient></Defs><Rect width="100%" height="100%" fill={`url(#${ownerGradient})`} /></Svg>
        {platform === 'phone' ? <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: 16, borderWidth: 1, borderColor: 'rgba(92,179,226,.18)'}} /> : null}
        <View style={{paddingTop: 10, paddingBottom: 2, paddingHorizontal: 14, flexDirection: 'row', alignItems: 'center', gap: 10}}><ServerMark variant="editorPhone" /><SelectionText size={13.5} weight={800}>{defaultI18n.t('next.selection.owner')}</SelectionText><View style={{flex: 1}} />{props.ownerTag}</View>
        {owners.map(entry => row(entry, true))}
      </View> : null}
    </SelectionSurface>;
  }
  if (menu === 'queue') return <SelectionSurface testID={controlID(props.testID, 'menu', menu)} open={props.menu !== null} onOpenChange={open => {if (!open) props.onMenuChange(null);}}
    title={selectionText('next.selection.native.queueTitle', {items: props.selectionLabel ?? selectionText('next.selection.native.titles', {count: props.count})})}
    description={selectionText('next.selection.native.queueOrder')} initialFocusRef={queueFirst} returnFocusRef={props.returnFocusRef ?? retainedReturn.current}>
    {items.filter((entry): entry is Extract<MenuEntry, {onSelect: unknown}> => 'onSelect' in entry).map((entry, index) => <MenuRow key={entry.id} testID={`${controlID(props.testID, 'menu', menu)}-item-${entry.id}`} ref={index === 0 ? queueFirst : undefined} label={entry.label} icon={entry.icon} disabled={entry.disabled} description={entry.description ?? selectionText(entry.id === 'playNext' ? 'next.selection.native.queueNextDescription' : 'next.selection.native.queueLaterDescription')} onPress={() => {if (!entry.disabled) {entry.onSelect(); props.onMenuChange(null);}}} style={{minHeight: isTV ? 84 : 56, paddingHorizontal: isTV ? 0 : 22}} />)}
  </SelectionSurface>;
  return <Menu testID={controlID(props.testID, 'menu', menu)} open={props.menu !== null} onOpenChange={open => {if (!open) props.onMenuChange(null);}}
    title={props.menuTitle ?? selectionText('next.selection.native.selected', {count: props.count})}
    items={items} returnFocusRef={props.returnFocusRef ?? retainedReturn.current} presentation={isTV ? 'sideSheet' : 'sheet'} />;
}
function useSelectionBack(onCancel: () => void, menu: SelectionBarProps['menu']) {
  useEffect(() => {
    if (menu !== null) return;
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {onCancel(); return true;});
    return () => subscription.remove();
  }, [onCancel, menu]);
}
function activate(props: SelectionBarProps, id: SelectionAction) {
  if (id === 'queue' || id === 'watched' || id === 'more') props.onMenuChange(id);
  else props.onAction(id);
}

/** Header replaces its host toolbar. The phone action bar occupies the shell's tab-bar slot. */
export function SelectionBar(props: SelectionBarProps) {
  const {isTV, colors, textScale} = useTheme();
  const moreRef = useRef<View>(null), queueRef = useRef<View>(null), watchedRef = useRef<View>(null);
  useSelectionBack(props.onCancel, props.menu);
  const tintId = useId().replace(/:/g, '');
  const actions = permitted(props.actions, isTV, props.count > 200);
  if (!isTV) return <>
    <View style={{paddingTop: props.topInset ?? 0, backgroundColor: 'rgba(7,16,24,.9)', borderBottomWidth: 1, borderBottomColor: 'rgba(92,179,226,.25)'}}>
      <View style={[styles.header, props.selectAllUnavailable && {height: 62}]}>
        <Button testID={controlID(props.testID, 'dismiss')} variant="text" size="small" style={{paddingHorizontal: 0}} textStyle={{fontSize: 16.5 * textScale, color: colors.textSecondary}} onPress={props.onCancel}>{selectionText('next.selection.native.cancel')}</Button>
        <View pointerEvents="none" style={styles.count}><SelectionText size={17} weight={800}>{selectionText('next.selection.native.selected', {count: props.count})}</SelectionText></View>
        {props.selectAllUnavailable ? <View style={{marginLeft: 'auto', alignItems: 'flex-end', maxWidth: '50%'}}>
          <Button testID={controlID(props.testID, 'select-all')} variant="text" size="small" disabled accessibilityLabel={props.selectAllUnavailable.label} accessibilityHint={props.selectAllUnavailable.reason} accessibilityState={{disabled: true}} textStyle={{fontSize: 16 * textScale}}>{props.selectAllUnavailable.label}</Button>
          <SelectionText size={11} secondary>{props.selectAllUnavailable.reason}</SelectionText>
        </View> : props.onSelectAll && props.selectAllLabel ? <Button testID={controlID(props.testID, 'select-all')} variant="text" size="small" style={{marginLeft: 'auto', paddingHorizontal: 0}} textStyle={{fontSize: 16 * textScale}} onPress={props.onSelectAll}>{props.selectAllLabel}</Button> : null}
      </View>
      {props.subtitle ? <View style={{paddingHorizontal: 20, paddingBottom: 10}}><SelectionText size={13} weight={600} secondary>{props.subtitle}</SelectionText></View> : null}
    </View>
    <SelectionMenus {...props} />
  </>;
  return <>
    <View testID={props.testID} style={styles.tv}>
      <Svg style={StyleSheet.absoluteFill} width="100%" height="100%" accessible={false} pointerEvents="none"><Defs><LinearGradient id={tintId} x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={colors.accent} stopOpacity={.22}/><Stop offset="1" stopColor={colors.accent} stopOpacity={.12}/></LinearGradient></Defs><Rect width="100%" height="100%" rx={22} fill={`url(#${tintId})`}/></Svg>
      <View style={{flexDirection: 'row', alignItems: 'center', paddingLeft: 12, paddingRight: 18}}>
        <SelectionText size={28} weight={800} style={{letterSpacing: -0.56}}>{selectionText('next.selection.native.selected', {count: props.count})}</SelectionText>
        {props.subtitle ? <SelectionText size={20} weight={600} secondary style={{marginLeft: 12}}>{props.subtitle}</SelectionText> : null}
      </View>
      <View style={{height: 44, width: 2, backgroundColor: 'rgba(92,179,226,.35)', marginHorizontal: 6}} />
      {mainTV.filter(id => props.fourthSlot !== 'watched' || id !== 'myList').map(id => {const offer = actions.find(action => action.id === id);return offer ? <Button key={id} testID={controlID(props.testID, 'action', id)} ref={id === 'more' ? moreRef : id === 'queue' ? queueRef : id === 'watched' ? watchedRef : undefined} variant={id === 'play' ? 'primary' : 'glass'} size="compact" icon={icons[id]} iconOnly={id === 'more'} disabled={offer.disabled} accessibilityLabel={label(id)} accessibilityHint={offer.reason}
        style={{height: 64, borderRadius: 14, paddingHorizontal: id === 'more' ? 0 : id === 'play' ? 30 : 24}} textStyle={{fontSize: (id === 'play' ? 24 : 22) * textScale}} onPress={() => {if (!offer.disabled) activate(props, id);}}>{label(id)}</Button> : null;})}
      <View style={{flex: 1}} />
      {props.selectAllUnavailable ? <View style={{alignItems: 'flex-end', maxWidth: 260, gap: 2}}>
        <Button testID={controlID(props.testID, 'select-all')} variant="glass" size="compact" disabled accessibilityLabel={props.selectAllUnavailable.label} accessibilityHint={props.selectAllUnavailable.reason} accessibilityState={{disabled: true}} style={{height: 54, paddingHorizontal: 24}} textStyle={{fontSize: 22 * textScale}}>{props.selectAllUnavailable.label}</Button>
        <SelectionText size={16} secondary>{props.selectAllUnavailable.reason}</SelectionText>
      </View> : props.onSelectAll && props.selectAllLabel ? <Button testID={controlID(props.testID, 'select-all')} variant="glass" size="compact" onPress={props.onSelectAll} style={{height: 64, paddingHorizontal: 24}} textStyle={{fontSize: 22 * textScale}}>{props.selectAllLabel}</Button> : null}
      <Button testID={controlID(props.testID, 'dismiss')} variant="glass" size="compact" onPress={props.onCancel} style={{height: 64, paddingHorizontal: 24}} textStyle={{fontSize: 22 * textScale}}>{selectionText('next.selection.native.done')}</Button>
    </View>
    <SelectionMenus {...props} returnFocusRef={props.menu === 'more' ? moreRef : props.menu === 'queue' ? queueRef : props.menu === 'watched' ? watchedRef : undefined} />
  </>;
}

export function SelectionPhoneActionBar(props: Pick<SelectionBarProps, 'actions' | 'onAction' | 'onMenuChange' | 'fourthSlot'> & {bottomInset?: number}) {
  const {isTV, colors} = useTheme();
  if (isTV) return null;
  const recipe = glassRecipe();
  const ids = phoneMain.map(id => id === 'myList' ? props.fourthSlot ?? 'myList' : id);
  return <View style={[styles.phoneActions, {height: 50 + (props.bottomInset ?? 34)}]}>
    {recipe.nativeBlur ? <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={colors.raised} /> : null}
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: 'rgba(14,36,52,.94)', borderTopWidth: 1, borderTopColor: 'rgba(92,179,226,.3)'}]} />
    {ids.map(id => {
      const offer = props.actions.find(action => action.id === id);
      if (!offer) return <View key={id} style={{flex: 1}} />;
      return <Focusable key={id} accessibilityRole="button" accessibilityLabel={label(id)} accessibilityHint={offer.reason} disabled={offer.disabled}
        onPress={() => {if (offer.disabled) return; if (id === 'queue' || id === 'watched' || id === 'more') props.onMenuChange(id);else props.onAction(id);}}
        style={{flex: 1, height: 44, alignItems: 'center', paddingTop: id === 'play' ? 0 : 2, gap: 4, opacity: offer.disabled ? .42 : 1}}>
        <View style={id === 'play' ? {width: 34, height: 28, borderRadius: 9, backgroundColor: colors.text, alignItems: 'center', justifyContent: 'center'} : {height: 24}}><Icon name={icons[id]} size={id === 'play' ? 20 : 24} color={id === 'play' ? colors.ink : colors.textSecondary} /></View>
        <SelectionText size={10.5} weight={700}>{label(id)}</SelectionText>
      </Focusable>;
    })}
  </View>;
}
const styles = StyleSheet.create({header: {height: 46, paddingHorizontal: 18, flexDirection: 'row', alignItems: 'center'}, count: {position: 'absolute', left: 0, right: 0, alignItems: 'center'}, tv: {height: 92, paddingLeft: 12, paddingRight: 16, borderRadius: 22, flexDirection: 'row', alignItems: 'center', gap: 14, borderWidth: 2, borderColor: 'rgba(92,179,226,.45)'}, phoneActions: {flexDirection: 'row', paddingTop: 9, paddingHorizontal: 8, overflow: 'hidden'}});
