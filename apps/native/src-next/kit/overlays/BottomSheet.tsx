import {OverlayPaintDescendants, useOverlayPaintMode} from './OverlayPaintContext';
import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Modal, ScrollView, StyleSheet, TextInput, View, useWindowDimensions} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button} from '../controls/Button';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {useKeyboardHeight} from '../../platform/keyboard';
import {TVDesignCanvas} from './TVDesignCanvas';
import {useOverlayLifecycle} from './OverlayHost';
import {releaseSheet, resistSheet, sheetHeights} from './geometry';
import {alpha, FocusBoundary, PanelBackground, SurfaceHeader, usePresence} from './surface';
import type {BottomSheetProps, OverlayReason} from './types';

export function BottomSheet({open, onOpenChange, title, description, children, footer, headerAction, headerContext, headerContent, initialFocusRef, returnFocusRef, restoreFocus = true, accessibilityLabel, testID, style, contentStyle, titleType, titleTextStyle, descriptionTextStyle, detents, width, detent, defaultDetent = 0, onDetentChange, dismissOnOutsidePress = true, dismissOnBack = true, dismissOnGesture = true, scrollable = true, showClose = true, showGrabber = true, fitContent = false, contentFooterGap = 16, keyboardAvoiding = false, footerPadding, showFooterSeparator = true, backdropOpacity = 0.64, host = 'modal', presentation = 'sheet', onExited}: BottomSheetProps) {
  const theme = useTheme(), paint = useOverlayPaintMode();
  const page = host === 'inline' && presentation === 'page';
  const side = !page && theme.platform !== 'phone';
  const sideWidth = width ?? (theme.isTV ? 640 : 440);
  const motion = useMotion();
  const viewport = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const [windowHeight, setWindowHeight] = useState(viewport.height);
  const avoidKeyboard = keyboardAvoiding && !side && !page;
  // The sheet rises as the keyboard starts, on its own duration (iOS and Android alike).
  const keyboardHeight = useKeyboardHeight(avoidKeyboard && open);
  // A window that already shrank for the keyboard (an Android resize) isn't covered twice.
  const keyboardInset = Math.max(0, keyboardHeight - Math.max(0, viewport.height - windowHeight));
  const keyboardTop = keyboardHeight > 0 ? windowHeight - keyboardInset : undefined;
  const availableHeight = Math.min(viewport.height, windowHeight - keyboardInset);
  const [bodyHeight, setBodyHeight] = useState(0);
  const [headerHeight, setHeaderHeight] = useState(64);
  const [footerHeight, setFooterHeight] = useState(0);
  const scrollRef = useRef<ScrollView>(null);
  const scrollOffset = useRef(0);
  const revealFocusedInput = () => {
    if (!avoidKeyboard || keyboardTop === undefined || !open) return;
    const input = TextInput.State?.currentlyFocusedInput();
    const scroll = scrollRef.current;
    if (!input || !scroll) return;
    // Both measurements use window coordinates. The generic ScrollView keyboard
    // helper mixes content-relative input Y with screen Y inside an inset sheet.
    scroll.getNativeScrollRef()?.measureInWindow((_x, top, _width, height) => {
      input.measureInWindow((_inputX, inputTop, _inputWidth, inputHeight) => {
        if (TextInput.State.currentlyFocusedInput() !== input) return;
        const visibleBottom = Math.min(top + height, keyboardTop) - footerHeight - contentFooterGap;
        const obscured = inputTop + inputHeight - visibleBottom;
        if (obscured > 0) scroll.scrollTo({y: scrollOffset.current + obscured, animated: true});
      });
    });
  };
  const contentPadding = footer == null ? insets.bottom + (fitContent ? 0 : 20) : footerHeight + contentFooterGap;
  // A fitted phone must include the same clearance its ScrollView reserves.
  // Include the sheet border too, so the last content pixel clears the footer.
  const footerSpace = footer == null ? insets.bottom : side ? footerHeight : contentPadding + styles.sheet.borderTopWidth;
  const naturalHeight = bodyHeight + headerHeight + footerSpace + 8;
  const heights = useMemo(() => sheetHeights(fitContent ? [naturalHeight] : detents, availableHeight, insets.top), [fitContent, naturalHeight, detents, availableHeight, insets.top]);
  const [internal, setInternal] = useState(defaultDetent);
  const index = Math.max(0, Math.min(heights.length - 1, detent ?? internal));
  const [settled, setSettled] = useState(index);
  const max = heights[heights.length - 1];
  const shown = useSharedValue(0);
  const opacity = useSharedValue(0);
  const start = useSharedValue(0);
  const closeRef = useRef<View>(null);
  const done = useRef(false);
  const releaseVelocity = useRef<{index: number; velocity: number} | undefined>(undefined);
  const closeVelocity = useRef(0);
  const sideProgress = useSharedValue(0);
  const present = usePresence(open, motion.reduce ? 160 : theme.isTV ? 240 : 500);
  const exitedRef = useRef(onExited); exitedRef.current = onExited;
  const wasPresent = useRef(present);
  useEffect(() => {if (wasPresent.current && !present) exitedRef.current?.(); wasPresent.current = present;}, [present]);
  const dismiss = useCallback((reason: OverlayReason) => {
    if ((reason === 'back' || reason === 'escape') && !dismissOnBack) return;
    done.current = reason === 'close-button' || reason === 'action';
    onOpenChange(false, reason);
  }, [onOpenChange, dismissOnBack]);
  useOverlayLifecycle(present && !page, true, dismiss, initialFocusRef ?? closeRef, returnFocusRef, motion.reduce ? 160 : theme.isTV ? 320 : 500, testID, restoreFocus && !page);
  useEffect(() => {
    // tv-more 4.7: a TV side sheet slides at full opacity; reduced motion keeps the fade.
    opacity.value = motion.reduce ? withTiming(open ? 1 : 0, motion.timing()) : side && theme.isTV ? 1 : open ? withTiming(1, motion.timing('standard')) : 1;
    const target = open ? heights[index] : 0;
    const spring = motion.spring('settle', !open ? -closeVelocity.current : releaseVelocity.current?.index === index ? -releaseVelocity.current.velocity : 0);
    releaseVelocity.current = undefined;
    closeVelocity.current = 0;
    sideProgress.value = withTiming(open ? 1 : 0, {...motion.timing(open ? 'slow' : 'quick', open ? 'out' : 'in'), duration: motion.reduce ? 160 : open ? 320 : 240});
    shown.value = motion.reduce ? target : !open && done.current ? withTiming(0, motion.timing('slow', 'in')) : withSpring(target, spring);
    if (open) {setSettled(index); done.current = false;}
  }, [open, heights, index, motion, shown, opacity, sideProgress, side, theme.isTV]);
  const choose = useCallback((next: number, velocityY = 0) => {
    const targetIndex = Math.max(0, Math.min(heights.length - 1, next));
    releaseVelocity.current = targetIndex === index ? undefined : {index: targetIndex, velocity: velocityY};
    if (detent === undefined) setInternal(targetIndex);
    onDetentChange?.(targetIndex);
    const spring = motion.spring('settle', -velocityY);
    const actualIndex = detent === undefined ? targetIndex : index;
    shown.value = motion.reduce ? heights[actualIndex] : withSpring(heights[actualIndex], spring);
    if (motion.reduce) {opacity.value = 0; opacity.value = withTiming(1, motion.timing());}
    setSettled(actualIndex);
  }, [heights, index, detent, onDetentChange, motion, shown, opacity]);
  const released = useCallback((height: number, velocityY: number) => {
    const result = releaseSheet(height, velocityY, heights);
    if (result.closed && dismissOnGesture) {closeVelocity.current = velocityY; dismiss('gesture');} else choose(result.index, velocityY);
  }, [heights, dismissOnGesture, dismiss, choose]);
  const pan = Gesture.Pan().enabled(!page && !side && open && paint.interactive).onBegin(() => {cancelAnimation(shown); start.value = shown.value;}).onUpdate(event => {
    shown.value = resistSheet(start.value - event.translationY, max);
  }).onEnd((event, success) => {if (success) runOnJS(released)(shown.value, event.velocityY);}).onFinalize((_event, success) => {
    if (!success) runOnJS(choose)(index);
  });
  const sheetStyle = useAnimatedStyle(() => ({opacity: opacity.value, transform: [{translateY: max - shown.value}]}));
  const scrimStyle = useAnimatedStyle(() => ({opacity: motion.reduce ? opacity.value : side ? sideProgress.value : Math.max(0, Math.min(1, shown.value / max))}));
  const footerStyle = useAnimatedStyle(() => ({opacity: opacity.value, transform: side || motion.reduce ? [] : [{translateY: shown.value - max + Math.max(0, (1 - shown.value / heights[0]) * footerHeight)}]}));
  const tvStyle = useAnimatedStyle(() => ({opacity: opacity.value, transform: motion.reduce ? [] : [{translateX: (1 - sideProgress.value) * sideWidth}]}));
  const close = <Button testID={testID ? `${testID}-close` : undefined} focusId="close" ref={closeRef} variant="text" size="small" style={theme.isTV ? undefined : {height: 24, paddingHorizontal: 0}} textStyle={{color: theme.colors.textSecondary}} hitSlop={theme.isTV ? undefined : 10} onPress={() => dismiss('close-button')}>{defaultI18n.t('action.done')}</Button>;
  if (!present || !paint.visible) return null;
  const body = <View style={[styles.body, contentStyle]}>{scrollable ? <ScrollView ref={scrollRef} onLayout={revealFocusedInput} onScroll={event => {scrollOffset.current = event.nativeEvent.contentOffset.y;}} scrollEventThrottle={16} onContentSizeChange={(_width, height) => setBodyHeight(Math.max(0, height - contentPadding))} scrollEnabled={page || side || settled === heights.length - 1} keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingBottom: page ? 0 : contentPadding}}>{children}</ScrollView> : <View style={fitContent ? undefined : [styles.body, {paddingBottom: page || side ? 0 : contentPadding}]} onLayout={event => setBodyHeight(event.nativeEvent.layout.height)}>{children}</View>}</View>;
  const surface = <TVDesignCanvas><View pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive} importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={styles.root} onLayout={event => setWindowHeight(event.nativeEvent.layout.height)}><View testID={testID ? `${testID}-keyboard-boundary` : undefined} style={[styles.root, {marginBottom: keyboardInset}]} accessibilityViewIsModal={!page} onAccessibilityEscape={() => {if (!page) dismiss('escape');}}>
      <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.isTV ? theme.colors.ink : '#03080c', backdropOpacity)}, scrimStyle, page ? {display: 'none'} : undefined]} pointerEvents="none"/>
      <View style={[StyleSheet.absoluteFill, page ? {display: 'none'} : undefined]} pointerEvents={page ? 'none' : 'auto'} onStartShouldSetResponder={() => !page} onResponderRelease={() => {if (!page && dismissOnOutsidePress) dismiss('outside');}} accessible={false}/>
      {/* One native content ancestry across compact/medium. Changing geometry
          cannot recreate the ScrollView or a focused TextInput/IME session. */}
      <Animated.View testID={testID} accessibilityLabel={accessibilityLabel} style={[page ? styles.root : side ? styles.tv : styles.sheet,
        page ? undefined : side ? {width: sideWidth, paddingTop: theme.isTV ? 64 : 24, paddingRight: theme.isTV ? 96 : 20, paddingBottom: theme.isTV ? 56 : 20, paddingLeft: theme.isTV ? 56 : 20}
          : {height: max, borderColor: alpha(theme.colors.text, 0.08)},
        {backgroundColor: theme.colors.raised}, page ? undefined : side ? tvStyle : sheetStyle, style]}>
        {theme.isTV && <PanelBackground/>}
        <FocusBoundary modal={!page} testID={testID} style={styles.root}>
          <GestureDetector gesture={pan}><Animated.View style={page ? {display: 'none'} : undefined} onLayout={event => setHeaderHeight(event.nativeEvent.layout.height)}>
            <View style={side ? {display: 'none'} : undefined} accessible={!side} accessibilityRole="adjustable" accessibilityLabel={accessibilityLabel ?? (typeof title === 'string' ? title : undefined)} accessibilityValue={{min: 1, max: heights.length, now: index + 1, text: defaultI18n.t('next.overlays.native.sheetPosition', {position: index + 1, count: heights.length})}}
              accessibilityActions={[{name: 'increment', label: defaultI18n.t('next.overlays.native.expandSheet')}, {name: 'decrement', label: defaultI18n.t('next.overlays.native.collapseSheet')}, ...(dismissOnGesture ? [{name: 'dismiss' as const, label: defaultI18n.t('action.close')}] : [])]}
              onAccessibilityAction={event => {if (event.nativeEvent.actionName === 'dismiss') {if (dismissOnGesture) dismiss('gesture');} else choose(index + (event.nativeEvent.actionName === 'increment' ? 1 : -1));}}>
              {showGrabber && <View style={[styles.grabber, {backgroundColor: theme.colors.lineStrong}]}/>}
            </View>
            {headerContext}{headerContent !== undefined ? headerContent : <SurfaceHeader side={side} inset={!side} title={title} description={description} titleType={titleType} titleTextStyle={titleTextStyle} descriptionTextStyle={descriptionTextStyle} action={headerAction ?? (!side && showClose ? close : undefined)}/>}
          </Animated.View></GestureDetector>
          {body}
          {!page && (footer != null || side && showClose) && <Animated.View onLayout={event => setFooterHeight(event.nativeEvent.layout.height)}
            style={[side ? {paddingTop: 20} : styles.footer,
              side ? undefined : {backgroundColor: theme.colors.raised, paddingTop: footerPadding?.top ?? 14, paddingBottom: footerPadding ? keyboardTop !== undefined ? footerPadding.keyboardBottom : Math.max(insets.bottom, footerPadding.bottom) : (keyboardInset > 0 ? 0 : insets.bottom) + 16,
                borderTopWidth: showFooterSeparator ? 1 : 0, borderTopColor: alpha(theme.colors.text, 0.08)}, footerStyle]}>{footer ?? close}</Animated.View>}
        </FocusBoundary>
      </Animated.View>
    </View></View></TVDesignCanvas>;
  return <OverlayPaintDescendants>{host === 'inline' ? surface : <Modal transparent visible animationType="none" statusBarTranslucent navigationBarTranslucent onRequestClose={() => dismiss('back')} supportedOrientations={['portrait', 'landscape']}>{surface}</Modal>}</OverlayPaintDescendants>;
}
const styles = StyleSheet.create({
  root: {flex: 1}, body: {flex: 1, minHeight: 0},
  sheet: {position: 'absolute', bottom: 0, left: 0, right: 0, borderTopLeftRadius: 24, borderTopRightRadius: 24, paddingTop: 8, overflow: 'hidden', borderTopWidth: 1, shadowColor: '#000', shadowOpacity: 0.8, shadowRadius: 30, shadowOffset: {width: 0, height: -12}},
  grabber: {width: 38, height: 5, borderRadius: 3, alignSelf: 'center', marginBottom: 10},
  footer: {position: 'absolute', bottom: 0, left: 0, right: 0, paddingTop: 14, paddingHorizontal: 16, borderTopWidth: 1},
  tv: {position: 'absolute', right: 0, top: 0, bottom: 0, width: 640, paddingTop: 64, paddingRight: 96, paddingBottom: 56, paddingLeft: 56},
});
