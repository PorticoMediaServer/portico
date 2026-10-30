import React, {useRef, useState, type ReactNode} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {MenuRow, Popover} from '../overlays';
import type {FocusRef} from '../overlays';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {alpha, browseType} from './presentation';
import type {LibraryChoice, LibraryHeaderProps} from './types';

const t = defaultI18n.t;

export function LibraryHeader({name, counts, switcher, menu, ambient, children, testID}: LibraryHeaderProps) {
  const theme = useTheme();
  return <View testID={testID}>
    {ambient && <View pointerEvents="none" style={{position: 'absolute', inset: 0}}>{ambient}</View>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 40 : 14, paddingTop: theme.isTV ? 56 : 36, paddingLeft: theme.gutter, paddingRight: theme.isTV ? theme.layout.gutter.tvRight : theme.gutter}}>
      <View style={{flex: theme.isTV ? undefined : 1, minWidth: 0}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
          <Text accessibilityRole="header" numberOfLines={1} style={[browseType(theme, theme.isTV ? 44 : 56, 800, -.045, theme.isTV ? 'normal' : 56), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text>
          {switcher}
        </View>
        {!!counts?.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginTop: 12}}>{counts.map(part => <Text key={part.id} style={[browseType(theme, theme.isTV ? 22 : 15), {color: theme.colors.textTertiary}]}>{part.text}</Text>)}</View>}
      </View>
      {theme.isTV && children}
      {menu}
    </View>
    {!theme.isTV && children}
  </View>;
}

export function StickyBrowseHeader({name, switcher, menu, ambient, children, onLibraries, safeArea = true, style, testID}: Omit<LibraryHeaderProps, 'counts'> & {onLibraries: () => void; safeArea?: boolean; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <View testID={testID} style={[{paddingTop: safeArea ? insets.top : 0, backgroundColor: alpha(theme.colors.ink, ambient ? .62 : .84), borderBottomColor: alpha(theme.colors.text, .07), borderBottomWidth: 1, zIndex: theme.layers.sticky}, style]}>
    {ambient && <View pointerEvents="none" style={{position: 'absolute', inset: 0}}>{ambient}</View>}
    {glassRecipe().nativeBlur && <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <View style={{height: 44, paddingLeft: 12, paddingRight: 10, flexDirection: 'row', alignItems: 'center'}}>
      <Focusable accessibilityRole="button" focusKind="row" onPress={onLibraries} accessibilityLabel={t('next.browse.native.libraries')} style={{height: 44, justifyContent: 'center'}}>
        <View style={{flexDirection: 'row', alignItems: 'center'}}><Icon name="i-left" size={22} color={theme.colors.textSecondary}/><Text style={[browseType(theme, 16, 600), {color: theme.colors.textSecondary}]}>{t('next.browse.native.libraries')}</Text></View>
      </Focusable>
      <View pointerEvents="box-none" style={{position: 'absolute', left: 118, right: 118, top: 0, height: 44, alignItems: 'center', justifyContent: 'center'}}>
        {switcher || <Text numberOfLines={1} style={[browseType(theme, 17, 700), {color: theme.colors.text}]}>{name}</Text>}
      </View>
      <View style={{marginLeft: 'auto'}}>{menu}</View>
    </View>
    {children}
  </View>;
}

/** Its trigger can be the centred phone name, or only the chevron beside a large header. */
export function LibrarySwitcher({libraries, value, onValueChange, open: suppliedOpen, onOpenChange, trigger = 'name', testID, returnFocusRef}: {
  libraries: readonly LibraryChoice[]; value: string; onValueChange: (id: string) => void;
  open?: boolean; onOpenChange?: (open: boolean) => void; trigger?: 'name' | 'chevron'; testID?: string; returnFocusRef?: FocusRef;
}) {
  const theme = useTheme(), anchorRef = useRef<View>(null), [localOpen, setLocalOpen] = useState(false);
  const open = suppliedOpen ?? localOpen, current = libraries.find(library => library.id === value);
  const setOpen = (next: boolean) => {if (suppliedOpen === undefined) setLocalOpen(next); onOpenChange?.(next);};
  return <>
    <Focusable ref={anchorRef} testID={testID} accessibilityRole="button" focusKind="row" onPress={() => setOpen(!open)} accessibilityLabel={current ? t('next.browse.native.switchLibraryNamed', {name: current.name}) : t('next.browse.native.switchLibrary')} accessibilityState={{expanded: open}}
      style={{height: theme.isTV ? 64 : 44, maxWidth: '100%', paddingHorizontal: trigger === 'chevron' ? 8 : 0, minWidth: trigger === 'chevron' ? 40 : 0, justifyContent: 'center'}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 4, maxWidth: '100%'}}>
        {trigger === 'name' && current && <Text numberOfLines={1} style={[browseType(theme, theme.isTV ? 44 : 17, theme.isTV ? 800 : 700), {color: theme.colors.text, flexShrink: 1}]}>{current.name}</Text>}
        <Icon name="i-down" size={theme.isTV ? 30 : trigger === 'chevron' ? 22 : 16} color={theme.isTV ? theme.colors.textTertiary : theme.colors.text} style={{transform: [{rotate: open ? '180deg' : '0deg'}]}}/>
      </View>
    </Focusable>
    <Popover testID={testID ? `${testID}-surface` : undefined} open={open} onOpenChange={setOpen} anchorRef={anchorRef} returnFocusRef={returnFocusRef ?? anchorRef} title={t('next.browse.native.switchLibrary')} accessibilityLabel={t('next.browse.native.switchLibrary')} placement="bottom-center" width={theme.isTV ? 520 : 252} offset={4} modal backdropOpacity={.62}>
      {libraries.filter(library => !theme.isTV || library.tv !== false).map(library => <MenuRow focusId={library.id} testID={testID ? `${testID}-library-${encodeURIComponent(library.id)}` : undefined} key={library.id} label={library.name} trailing={theme.isTV ? library.count : undefined} description={theme.isTV ? undefined : library.count} selected={library.id === value} accessibilityRole="radio" accessibilityState={{checked: library.id === value}} variant={theme.isTV ? 'tv' : 'compact'} onPress={() => {onValueChange(library.id); setOpen(false);}}/>)}
    </Popover>
  </>;
}

type LibraryMenuCommon = {
  read?: boolean; canSaveView?: boolean; onSaveView: () => void; /** Present only where the host has a real count-aware bulk command. */ onMarkAll?: () => void; onEditPins: () => void;
  viewSummary?: ReactNode; count?: ReactNode; open?: boolean; onOpenChange?: (open: boolean) => void; testID?: string;
  /** O-162: Share and Copy link for this library (touch). */
  shareActions?: readonly Readonly<{id: string; label: string; icon: IconName; onSelect: () => void}>[];
};
export type LibraryMenuProps = LibraryMenuCommon & (
  | {owner: true; ownerTag: ReactNode; onLibrarySettings: () => void; onScan: () => void}
  | {owner: false; onLibrarySettings?: never; onScan?: never}
);

/** onMarkAll opens the host's count-aware confirmation, then its durable bulk command. */
export function LibraryMenu(props: LibraryMenuProps) {
  const theme = useTheme(), anchorRef = useRef<View>(null), [localOpen, setLocalOpen] = useState(false);
  const open = props.open ?? localOpen;
  const setOpen = (next: boolean) => {if (props.open === undefined) setLocalOpen(next); props.onOpenChange?.(next);};
  const action = (run: () => void) => () => {setOpen(false); run();};
  return <>
    <Button ref={anchorRef} testID={props.testID} variant="icon" icon="i-more" size="compact" iconOnly onPress={() => setOpen(!open)} accessibilityLabel={t('next.browse.native.libraryOptions')} accessibilityState={{expanded: open}}/>
    <Popover testID={props.testID ? `${props.testID}-surface` : undefined} open={open} onOpenChange={setOpen} anchorRef={anchorRef} returnFocusRef={anchorRef} title={t('next.browse.native.libraryOptions')} accessibilityLabel={t('next.browse.native.libraryOptions')} placement="bottom-end" width={theme.isTV ? 560 : 282} offset={2} modal backdropOpacity={.62}>
      <MenuRow focusId="save-view" testID={props.testID ? `${props.testID}-save-view` : undefined} label={t('next.browse.native.saveThisView')} icon="i-filter" trailing={props.viewSummary} variant="compact" disabled={props.canSaveView === false} onPress={action(props.onSaveView)}/>
      {props.onMarkAll && <MenuRow focusId="mark-all" testID={props.testID ? `${props.testID}-mark-all` : undefined} label={t(props.read ? 'next.browse.native.markAllRead' : 'next.browse.native.markAllWatched')} icon="i-check" trailing={props.count} variant="compact" onPress={action(props.onMarkAll)}/>}
      <MenuRow focusId="edit-pins" testID={props.testID ? `${props.testID}-edit-pins` : undefined} label={t('next.browse.native.editPins')} icon="i-updown" variant="compact" onPress={action(props.onEditPins)}/>
      {props.shareActions?.map(share => <MenuRow key={share.id} focusId={share.id} testID={props.testID ? `${props.testID}-${share.id}` : undefined} label={share.label} icon={share.icon} variant="compact" onPress={action(share.onSelect)}/>)}
      {props.owner && !theme.isTV && <>
        <View style={{height: 34, marginTop: 6, paddingHorizontal: 14, flexDirection: 'row', alignItems: 'center', borderTopWidth: 1, borderTopColor: alpha(theme.colors.accentStrong, .22), backgroundColor: alpha(theme.colors.accent, .16)}}>
          <Text style={[browseType(theme, 12, 700), {color: theme.colors.accentStrong}]}>{t('next.browse.native.server')}</Text>
          <View style={{marginLeft: 'auto'}}>{props.ownerTag}</View>
        </View>
        <MenuRow focusId="settings" testID={props.testID ? `${props.testID}-settings` : undefined} label={t('next.browse.native.librarySettings')} icon="i-cog" tone="owner" variant="compact" onPress={action(props.onLibrarySettings)}/>
        <MenuRow focusId="scan" testID={props.testID ? `${props.testID}-scan` : undefined} label={t('next.browse.native.scan')} icon="i-reload" tone="owner" variant="compact" onPress={action(props.onScan)}/>
      </>}
    </Popover>
  </>;
}
