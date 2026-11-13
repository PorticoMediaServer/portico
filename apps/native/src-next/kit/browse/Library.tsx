import React, {useRef, useState, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {TopBar} from '../layout/TopBar';
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

/** The phone library header: the shared top bar (kit/layout/TopBar) with "‹ Libraries", the switcher or name, ⋯ and the pivots. */
export function StickyBrowseHeader({name, switcher, menu, children, onLibraries, safeArea = true, testID}: Omit<LibraryHeaderProps, 'counts'> & {onLibraries: () => void; safeArea?: boolean}) {
  return <TopBar testID={testID} surface safeArea={safeArea} back={{label: t('next.browse.native.libraries'), onPress: onLibraries}} title={switcher ? undefined : name} center={switcher || undefined} trailing={menu}>{children}</TopBar>;
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
