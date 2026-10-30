import {OverlayPaintDescendants, useOverlayPaintMode} from './OverlayPaintContext';
import React, {forwardRef, useCallback, useEffect, useRef, useState} from 'react';
import {Modal, Pressable, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import Animated, {useAnimatedStyle} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, RadialGradient, Rect, Stop} from 'react-native-svg';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';
import {Focusable} from '../controls/Focusable';
import {BottomSheet} from './BottomSheet';
import {OverlayPortal, useOverlayFrame, useOverlayLifecycle} from './OverlayHost';
import {popoverRect} from './geometry';
import {rightEndMenuRect} from './anchored-menu-geometry';
import {touchMenuRect} from '../browse/touch-menu-geometry';
import {alpha, FocusBoundary, useSurfaceMotion} from './surface';
import type {MenuProps, MenuRowProps, OverlayReason, PopoverProps} from './types';

/** select17's compact heading; the caller supplies only the captured item's real facts. */
export function TouchMenuHeader({title, artwork, kind, year}: {title?: string; artwork?: React.ReactNode; kind?: string; year?: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingTop: 2, paddingHorizontal: 20, paddingBottom: 12}}>
    {artwork && <View style={{width: 40, height: 60, borderRadius: 6, overflow: 'hidden'}}>{artwork}</View>}
    <View style={{flex: 1, minWidth: 0}}><Text accessibilityRole="header" style={[nativeType({size: 17, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{title}</Text>
      {(kind || year) && <View style={{flexDirection: 'row', gap: 8, marginTop: 2}}>{[kind, year].filter(Boolean).map((fact, index) => <Text key={index} style={[nativeType({size: 13, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{fact}</Text>)}</View>}
    </View>
  </View>;
}

export const MenuRow = forwardRef<View, MenuRowProps & {tvFlyout?: boolean; touchMenu?: 'sheet' | 'popover'}>(function MenuRow({label, icon, description, selected, destructive, tone = 'default', trailing, variant, tvFlyout = false, touchMenu, style, onFocus, onBlur, onLongPress, onMenu, disabled, accessibilityLabel, accessibilityRole, accessibilityState, ...props}, ref) {
  const theme = useTheme();
  const [focused, setFocused] = useState(false);
  const grouped = variant === 'grouped';
  const tv = theme.isTV || variant === 'tv';
  const flyout = tv && tvFlyout;
  const ink = focused && tv && !flyout ? theme.colors.ink : destructive ? theme.colors.record : selected === false && !grouped && !tv ? theme.colors.textSecondary : theme.colors.text;
  const iconColor = focused && tv && !flyout ? theme.colors.ink : tone === 'owner' ? theme.colors.accentStrong : destructive ? theme.colors.record : theme.colors.textSecondary;
  return <Focusable {...props} focusKind={flyout ? 'row' : 'button'} focusRadius={tv ? 14 : 8} onLongPressAction={onMenu} ref={ref} disabled={disabled} onLongPress={onLongPress}
    accessibilityRole={accessibilityRole ?? (selected !== undefined ? 'menuitem' : 'button')} accessibilityState={{selected, disabled: disabled ?? undefined, ...accessibilityState}} accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : undefined)}
    onFocus={event => {setFocused(true); onFocus?.(event);}} onBlur={event => {setFocused(false); onBlur?.(event);}}
    hitSlop={props.hitSlop ?? (tv || grouped ? undefined : {top: 1, bottom: 1, left: 0, right: 0})} style={state => [{flexDirection: 'row', alignItems: 'center', gap: flyout ? 20 : touchMenu ? 14 : tv ? 14 : grouped ? 12 : 10, minHeight: flyout ? 76 : tv ? 72 : grouped ? 48 : 42, paddingLeft: touchMenu === 'sheet' ? tone === 'owner' ? 14 : 20 : flyout ? 20 : tv ? 28 : grouped ? 14 : 12, paddingRight: touchMenu === 'sheet' ? tone === 'owner' ? 14 : 20 : flyout ? 20 : tv ? 28 : touchMenu ? 12 : 14, paddingVertical: description ? 8 : touchMenu === 'sheet' ? 2 : 0, borderRadius: tv ? 14 : touchMenu === 'sheet' ? 0 : touchMenu ? 10 : grouped ? 0 : 8, opacity: disabled ? 0.38 : 1, backgroundColor: flyout ? focused ? alpha(theme.colors.text, .16) : 'transparent' : focused && tv ? theme.colors.text : state.pressed ? alpha(theme.colors.text, 0.1) : tv ? alpha(theme.colors.text, 0.14) : tone === 'owner' && !grouped && !touchMenu ? alpha(theme.colors.accent, 0.07) : 'transparent', borderWidth: 0}, typeof style === 'function' ? style(state) : style]}>
      {flyout && tone === 'owner' && <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 14, overflow: 'hidden', borderWidth: 1.5, borderColor: alpha(theme.colors.accentStrong, .2)}]}><Svg width="100%" height="100%"><Defs><LinearGradient id="flyout-owner" x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor="#2f86bb" stopOpacity={.22}/><Stop offset="1" stopColor="#1e5b52" stopOpacity={.2}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#flyout-owner)"/></Svg></View>}
      {!icon && selected !== undefined && <View style={{width: tv ? 28 : 18}}>{selected && <Icon name="i-check" size={tv ? 28 : 18} color={theme.colors.accentStrong}/>}</View>}
      {icon && <View style={grouped && !tv ? {width: 30, height: 30, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(tone === 'owner' ? theme.colors.accent : theme.colors.text, tone === 'owner' ? 0.16 : 0.08)} : undefined}><Icon name={icon} size={flyout ? 30 : tv ? 28 : touchMenu === 'sheet' ? 24 : 18} color={iconColor}/></View>}
      <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[nativeType({size: tv ? 26 : touchMenu === 'sheet' ? 16 : grouped ? 15.5 : 15, weight: tv && !flyout ? 700 : 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: ink}]}>{label}</Text>
        {description != null && (typeof description === 'string' || typeof description === 'number' ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{description}</Text> : <View style={{marginTop: 2}}>{description}</View>)}
      </View>
      {typeof trailing === 'string' || typeof trailing === 'number' ? <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{trailing}</Text> : trailing ?? (icon && selected ? <Icon name="i-check" size={tv ? 28 : 18} color={theme.colors.accentStrong}/> : undefined)}
    </Focusable>;
});

export function Popover({open, onOpenChange, anchorRef, title, headerContext, description, children, footer, initialFocusRef, returnFocusRef, restoreFocus = true, accessibilityLabel, testID, style, contentStyle, titleType, titleTextStyle, descriptionTextStyle, width = 282, modal = false, holdFocus: suppliedHoldFocus, showTitle = false, backdropOpacity = 0, placement = 'bottom-end', offset = 6, alignOffset = 0, dismissOnOutsidePress = true, touchContext = false}: PopoverProps & {touchContext?: boolean}) {
  const theme = useTheme(), paint = useOverlayPaintMode();
  // TV: an open popover holds the remote's focus (as the kit Menu does), so Down can't walk into the page behind it.
  const holdFocus = suppliedHoldFocus ?? (modal || theme.isTV);
  const viewport = useWindowDimensions();
  const hostFrame = useOverlayFrame();
  const insets = useSafeAreaInsets();
  const animation = useSurfaceMotion(open, 'menu');
  const progress = animation.progress;
  const backdropStyle = useAnimatedStyle(() => ({opacity: progress.value}));
  const [anchor, setAnchor] = useState<{x: number; y: number; width: number; height: number}>();
  const [height, setHeight] = useState(1);
  const dismiss = useCallback((reason: OverlayReason) => onOpenChange(false, reason), [onOpenChange]);
  useOverlayLifecycle(animation.present, modal, dismiss, initialFocusRef, returnFocusRef ?? anchorRef, animation.enterMs, testID, restoreFocus);
  useEffect(() => {
    if (!open) return;
    let live = true, frame: number | undefined;
    const measure = () => {
      anchorRef.current?.measureInWindow((x, y, width, height) => {if (live) setAnchor(previous => previous && previous.x === x && previous.y === y && previous.width === width && previous.height === height ? previous : {x, y, width, height});});
      if (touchContext || placement === 'right-end') frame = requestAnimationFrame(measure);
    };
    measure();
    return () => {live = false; if (frame !== undefined) cancelAnimationFrame(frame);};
  }, [open, anchorRef, viewport.width, viewport.height, insets.top, insets.bottom, insets.left, insets.right, touchContext, placement]);
  if (!animation.present || !paint.visible || !anchor) return null;
  const touchRect = touchContext ? touchMenuRect(anchor, viewport, height, insets) : undefined;
  const canvasScale = theme.isTV && placement === 'right-end' ? Math.min(viewport.width / theme.layout.designWidth.tv, viewport.height / theme.layout.designHeight.tv) : 1;
  if (canvasScale <= 0) return null;
  const rect = touchRect ?? (placement === 'right-end' ? rightEndMenuRect(anchor, viewport, {width, height}, insets, offset, alignOffset, canvasScale) : popoverRect(anchor, viewport, {width, height}, insets, placement, offset));
  const material = glassRecipe();
  const rightEnd = placement === 'right-end', tvFlyout = rightEnd && theme.isTV;
  // motion.html 2.12/3.7: grow from the point nearest what was tapped, not from the menu's centre.
  const growFrom = rightEnd ? undefined : touchRect ? [touchRect.side === 'right' ? 0 : rect.width, touchRect.arrowY, 0] : [Math.max(0, Math.min(rect.width, anchor.x + anchor.width / 2 - rect.x)), rect.y >= anchor.y + anchor.height / 2 ? 0 : rect.height, 0];
  const surface = <Animated.View pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive} importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} testID={testID} accessibilityLabel={accessibilityLabel ?? (typeof title === 'string' ? title : undefined)} accessibilityViewIsModal={modal} onAccessibilityEscape={() => dismiss('escape')} style={[{transformOrigin: growFrom, position: 'absolute', left: rightEnd ? 0 : rect.x - (modal ? 0 : hostFrame.x), top: rightEnd ? 0 : rect.y - (modal ? 0 : hostFrame.y), width: rect.width / canvasScale, maxHeight: (viewport.height - insets.top - insets.bottom) / canvasScale - 16, borderRadius: tvFlyout ? 24 : touchContext ? 16 : 14, backgroundColor: touchContext ? alpha('#162c3a', material.nativeBlur ? .97 : 1) : alpha(theme.colors.raisedBright, material.nativeBlur ? 0.96 : 1), borderWidth: tvFlyout ? 0 : 1, borderColor: alpha(theme.colors.text, 0.1), overflow: 'hidden', shadowColor: '#000', shadowOpacity: 0.8, shadowOffset: {width: 0, height: 16}, shadowRadius: 30, ...(tvFlyout ? {boxShadow: [{offsetX: 0, offsetY: 50, blurRadius: 100, spreadDistance: -24, color: '#000'}]} : {})}, animation.style, style]}>
    {material.nativeBlur && !tvFlyout && <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={touchContext ? 26 : 30} reducedTransparencyFallbackColor={touchContext ? '#162c3a' : theme.colors.raisedBright} pointerEvents="none"/>}
    {tvFlyout && <View pointerEvents="none" style={StyleSheet.absoluteFill}><Svg width="100%" height="100%"><Defs><LinearGradient id="tv-flyout" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#162c3a"/><Stop offset="1" stopColor="#0f2230"/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#tv-flyout)"/></Svg><View style={[StyleSheet.absoluteFill, {borderRadius: 24, borderWidth: 1.5, borderColor: alpha(theme.colors.text, .1)}]}/></View>}
    <FocusBoundary modal={holdFocus} testID={testID}><View onLayout={event => setHeight(event.nativeEvent.layout.height)} style={[tvFlyout ? {padding: 14} : touchContext ? {padding: 8} : {paddingVertical: 6}, contentStyle]}>
      {headerContext}
      {showTitle && title != null && <Text accessibilityRole="header" style={[titleType ? nativeType(titleType, theme.textScale) : touchContext ? nativeType({size: 15, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale) : theme.type.bodyStrong, {color: theme.colors.text, paddingHorizontal: touchContext ? 20 : 14, paddingTop: touchContext ? 6 : 8, paddingBottom: touchContext ? 10 : 8, marginHorizontal: touchContext ? -8 : 0, marginBottom: touchContext ? 6 : 0, borderBottomWidth: touchContext ? 1 : 0, borderBottomColor: alpha(theme.colors.text, .08)}, titleTextStyle]}>{title}</Text>}
      {description != null && <Text style={[theme.type.caption, {color: theme.colors.textSecondary, paddingHorizontal: 14}, descriptionTextStyle]}>{description}</Text>}
      {children}{footer}
    </View></FocusBoundary>
  </Animated.View>;
  const pointer = touchRect && <Animated.View pointerEvents="none" style={[backdropStyle, {position: 'absolute', left: touchRect.side === 'right' ? rect.x - 7 : rect.x + rect.width - 7, top: rect.y + touchRect.arrowY - 7, width: 14, height: 14, borderRadius: 3, backgroundColor: alpha('#162c3a', .97), transform: [{rotate: '45deg'}]}]}/>;
  const placedSurface = rightEnd ? <View pointerEvents="box-none" style={{position: 'absolute', left: rect.x - (modal ? 0 : hostFrame.x), top: rect.y - (modal ? 0 : hostFrame.y), width: rect.width / canvasScale, height: rect.height / canvasScale, transformOrigin: 'top left', transform: [{scale: canvasScale}]}}>{surface}</View> : surface;
  return <OverlayPaintDescendants>{modal ? <Modal transparent visible={open} animationType="none" onRequestClose={() => dismiss('back')} statusBarTranslucent navigationBarTranslucent>
    <View pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive} importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}><Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, backdropStyle]}>
      {touchContext ? [
        {left: 0, top: 0, right: 0, height: Math.max(0, anchor.y)},
        {left: 0, top: anchor.y + anchor.height, right: 0, bottom: 0},
        {left: 0, top: anchor.y, width: Math.max(0, anchor.x), height: anchor.height},
        {left: anchor.x + anchor.width, top: anchor.y, right: 0, height: anchor.height},
      ].map((rect, index) => <View key={index} style={{position: 'absolute', backgroundColor: alpha('#03080c', .58), ...rect}}/>) : <View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.ink, backdropOpacity)}]}/>}
    </Animated.View><Pressable accessible={false} focusable={false} style={StyleSheet.absoluteFill} onPress={() => {if (dismissOnOutsidePress) dismiss('outside');}}/>{pointer}{placedSurface}</View>
  </Modal> : <OverlayPortal rect={rect} onOutsidePress={dismissOnOutsidePress ? () => dismiss('outside') : undefined}>{placedSurface}</OverlayPortal>}</OverlayPaintDescendants>;
}

export function Menu({items, open, onOpenChange, title, description, footer, initialFocusRef, returnFocusRef, restoreFocus = true, accessibilityLabel, testID, style, contentStyle, titleType, titleTextStyle, descriptionTextStyle, anchorRef, width, presentation, placement, offset, alignOffset, initialFocusId, secondaryTitle, context, touchHeader, touchContext = false}: MenuProps & {touchContext?: boolean; touchHeader?: React.ReactNode}) {
  const theme = useTheme(), viewport = useWindowDimensions(), insets = useSafeAreaInsets();
  const first = useRef<View>(null);
  const actualPresentation = touchContext && !theme.isTV ? viewport.width < 600 ? 'sheet' : 'anchored' : presentation ?? (theme.isTV ? 'sideSheet' : anchorRef ? 'anchored' : 'sheet');
  const tvFlyout = theme.isTV && actualPresentation === 'anchored' && placement === 'right-end';
  const compactTouch = touchContext && !theme.isTV && actualPresentation === 'sheet';
  const grouped = !touchContext && (actualPresentation === 'sheet' || (actualPresentation === 'sideSheet' && !theme.isTV));
  if (actualPresentation === 'anchored' && !anchorRef) {if (open) throw new Error('An anchored menu requires an anchor ref.'); return null;}
  const rows = items.map((entry, index) => {
    if ('section' in entry) return <Text key={entry.id} style={[compactTouch ? nativeType({size: 13.5, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale) : theme.type.captionStrong, {color: entry.tone === 'owner' ? theme.colors.accentStrong : theme.colors.textTertiary, marginHorizontal: theme.isTV ? 0 : compactTouch && entry.tone === 'owner' ? 14 : 20, marginTop: compactTouch ? 10 : 16, marginBottom: compactTouch ? 2 : 8}]}>{entry.label}</Text>;
    if ('separator' in entry) return <View key={entry.id} style={grouped ? {height: 12} : {height: 1.5, backgroundColor: alpha(theme.colors.line, 0.9), marginHorizontal: tvFlyout ? 12 : 4, marginVertical: tvFlyout ? 8 : 6}}/>;
    const previousAction = items.slice(0, index).some(value => !('section' in value) && !('separator' in value) && !value.disabled);
    const initial = entry.id === initialFocusId || (!initialFocusId && !entry.disabled && !previousAction);
    const previous = items[index - 1];
    const next = items[index + 1];
    const groupStart = !previous || 'separator' in previous || 'section' in previous;
    const groupEnd = !next || 'separator' in next || 'section' in next;
    const corners = {borderTopLeftRadius: groupStart ? 14 : 0, borderTopRightRadius: groupStart ? 14 : 0, borderBottomLeftRadius: groupEnd ? 14 : 0, borderBottomRightRadius: groupEnd ? 14 : 0};
    return <View key={entry.id} style={[{marginHorizontal: grouped ? 16 : 0, marginBottom: actualPresentation === 'sideSheet' && theme.isTV ? 12 : 0, overflow: grouped ? 'hidden' : 'visible', backgroundColor: grouped ? theme.colors.raisedHigh : undefined}, corners]}>
      <MenuRow touchMenu={touchContext && !theme.isTV ? compactTouch ? 'sheet' : 'popover' : undefined} tvFlyout={tvFlyout} testID={testID ? `${testID}-item-${entry.id}` : undefined} focusId={entry.id} ref={initial ? first : undefined} variant={grouped ? 'grouped' : actualPresentation === 'sideSheet' ? 'tv' : 'compact'} style={touchContext && !theme.isTV ? {minHeight: 44} : undefined} label={entry.label} trailing={entry.trailing} description={entry.description} icon={entry.icon} disabled={entry.disabled} selected={entry.selected} destructive={entry.destructive} tone={entry.tone} onMenu={() => onOpenChange(false, 'back')} hasTVPreferredFocus={initial} onPress={() => {entry.onSelect(); if (entry.closeOnSelect !== false) onOpenChange(false, 'action');}}/>
      {grouped && !groupStart && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 56, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, 0.45)}}/>}
      {grouped && entry.tone === 'owner' && <View pointerEvents="none" style={[StyleSheet.absoluteFill, corners, {borderColor: alpha(theme.colors.accentStrong, 0.28), borderLeftWidth: 1, borderRightWidth: 1, borderTopWidth: groupStart ? 1 : 0, borderBottomWidth: groupEnd ? 1 : 0}]}/>}
    </View>;
  });
  const firstOwner = compactTouch ? items.findIndex(entry => 'section' in entry && entry.tone === 'owner') : -1;
  const body = firstOwner < 0 ? rows : <>{rows.slice(0, firstOwner)}<View style={{marginTop: 8, marginHorizontal: 12, paddingVertical: 4, borderRadius: 16, backgroundColor: '#0b1822', overflow: 'hidden', borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .18)}}><View pointerEvents="none" style={StyleSheet.absoluteFill}><Svg width="100%" height="100%"><Defs><RadialGradient id="touch-owner" cx="100%" cy="0%" rx="120%" ry="100%"><Stop offset="0" stopColor="#2f86bb" stopOpacity={.24}/><Stop offset="0.62" stopColor="#2f86bb" stopOpacity={0}/></RadialGradient></Defs><Rect width="100%" height="100%" fill="url(#touch-owner)"/></Svg></View>{rows.slice(firstOwner)}</View></>;
  const heading = secondaryTitle == null ? title : <>{title}{'  '}<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{secondaryTitle}</Text></>;
  if (actualPresentation === 'anchored') return <Popover showTitle={touchContext} touchContext={touchContext} modal={touchContext && !theme.isTV ? true : undefined} backdropOpacity={touchContext ? .58 : 0} holdFocus={touchContext || theme.isTV} restoreFocus={restoreFocus} open={open} onOpenChange={onOpenChange} anchorRef={anchorRef!} headerContext={context} placement={placement} offset={offset} alignOffset={alignOffset} width={touchContext ? 300 : width} title={title} description={description} titleType={titleType} titleTextStyle={titleTextStyle} descriptionTextStyle={descriptionTextStyle} footer={footer} initialFocusRef={initialFocusRef ?? first} returnFocusRef={returnFocusRef ?? anchorRef} accessibilityLabel={accessibilityLabel} testID={testID} style={style} contentStyle={contentStyle}><ScrollView keyboardShouldPersistTaps="handled">{body}</ScrollView></Popover>;
  return <BottomSheet showClose={touchContext ? false : undefined} backdropOpacity={touchContext ? .58 : undefined} restoreFocus={restoreFocus} open={open} onOpenChange={onOpenChange} title={heading} description={description} titleType={titleType} titleTextStyle={titleTextStyle} descriptionTextStyle={descriptionTextStyle} width={touchContext ? undefined : width} footer={footer ?? (theme.isTV ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}><Text style={{color: theme.colors.text}}>{defaultI18n.t('action.back')}</Text>{'  '}{defaultI18n.t('action.close')}</Text> : undefined)} headerContext={context} headerContent={touchContext && !theme.isTV ? touchHeader : undefined} initialFocusRef={initialFocusRef ?? first} returnFocusRef={returnFocusRef} accessibilityLabel={accessibilityLabel} testID={testID} style={style} contentStyle={contentStyle} fitContent>
    {compactTouch ? <View style={{paddingBottom: Math.max(0, 30 - insets.bottom)}}>{body}</View> : body}
  </BottomSheet>;
}
