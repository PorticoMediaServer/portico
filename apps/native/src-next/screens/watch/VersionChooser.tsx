import React, {useRef} from 'react';
import {Modal, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {shellLayout} from '../../shell/model';
import {Focusable} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {overlayPaintKey, RetainedOverlay, type OverlayPaintPermit} from '../../kit/overlays/SurfacePaintHost';
import {FocusBoundary} from '../../kit/overlays/surface';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {TVPanelSlide, TVPanelScrim, useTVPanelInteractive} from '../../kit/overlays/TVPanelMotion';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeWatchOverlay} from './presenters';

/** Approved phone9b / TV8b destination: immediate two-line choices, no tags,
 * checks, radio selection, confirmation step or remembered preference. */
export function NativeVersionChooser({value, choose, close, paintPermit}: {paintPermit?: OverlayPaintPermit; value: NativeWatchOverlay; choose: (id: string) => void; close: () => void}) {
  const theme = useTheme(), window = useWindowDimensions(), first = useRef<View>(null), done = useRef<View>(null), t = defaultI18n.t;
  const interactive = useTVPanelInteractive();
  const destination = value.destination, rows = destination?.rows ?? [];
  const selected = rows.findIndex(row => row.id === destination?.selectedRowId);
  const entry = selected >= 0 ? selected : Math.min(1, rows.length - 1);
  useOverlayLifecycle(theme.isTV, true, close, undefined, value.anchorRef, 0, 'watch-version-chooser');
  React.useEffect(() => {if (theme.isTV && interactive) first.current?.requestTVFocus?.();}, [theme.isTV, interactive, entry]);
  const body = <ScrollView testID="watch-version-rows" contentContainerStyle={{paddingTop: theme.isTV ? 4 : 0}}>
    {/* The server is analyzing the chosen disc title: the existing neutral word, and
        no claim that anything is playing or ready. */}
    {value.busy && theme.isTV && <Text testID="watch-version-preparing" style={[nativeType({size: theme.isTV ? 22 : 15, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale),
      {color: theme.colors.textTertiary, paddingHorizontal: theme.isTV ? 0 : 16, paddingBottom: 12}]}>{t('next.browse.native.markAllPreparing')}</Text>}
    <View style={theme.isTV ? undefined : {marginHorizontal: 16, borderRadius: 14, backgroundColor: theme.colors.raisedHigh, overflow: 'hidden'}}>
      {rows.map((row, index) => {
        const rowStyle = (focused: boolean) => ({minHeight: theme.isTV ? 54 : 64, paddingHorizontal: theme.isTV ? 14 : 16,
          paddingVertical: theme.isTV ? 14 : 10, marginBottom: theme.isTV ? 14 : 0, borderRadius: theme.isTV ? 12 : 0,
          backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent'});
        const rowContent = (focused: boolean) => <>
          {!theme.isTV && index > 0 && <View pointerEvents="none" style={{position: 'absolute', left: 16, right: 0, top: 0, height: 1, backgroundColor: '#3b566873'}}/>}
          <Text style={[nativeType({size: theme.isTV ? 22 : 15.5, weight: theme.isTV ? 600 : 700, tracking: theme.isTV ? 0 : -.01, lineHeight: 'normal'}, theme.textScale),
            {color: theme.isTV && !focused ? theme.colors.textSecondary : theme.colors.text}]}>{row.heading}</Text>
          {!!(row.detail || row.id === destination?.pendingRowId) && <Text style={[nativeType({size: theme.isTV ? 19 : 13, weight: 500, tracking: 0, lineHeight: theme.isTV ? 'normal' : 18.2}, theme.textScale),
            {marginTop: theme.isTV ? 2 : 3, color: theme.isTV && !focused ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{row.id === destination?.pendingRowId ? t('next.browse.native.markAllPreparing') : row.detail}</Text>}
        </>;
        return <Focusable key={row.id} ref={index === entry ? first : undefined} focusId={`watch-version-${row.id}`} testID={`watch-version-${row.id}`}
          focusKind="row" focusRadius={theme.isTV ? 12 : 0} accessibilityRole="button" hasTVPreferredFocus={theme.isTV && interactive && index === entry}
          onPress={() => {if (!value.busy) choose(row.id);}} style={theme.isTV ? ({focused}) => rowStyle(focused) : rowStyle(false)}>
          {theme.isTV ? ({focused}) => rowContent(focused) : rowContent(false)}
        </Focusable>;
      })}
    </View>
  </ScrollView>;
  const title = t('next.overlays.versionChooserTitle'), name = value.capture.entry.displayTitle ?? value.capture.entry.title;
  if (theme.isTV) {
    const canvas = shellLayout(window.width, window.height, true);
    const key = (label: string) => <View style={{height: 36, minWidth: 44, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: '#edf5fa4d', alignItems: 'center', justifyContent: 'center'}}>
      <Text style={[nativeType({size: 17, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text></View>;
    return <Modal transparent visible animationType="none" onRequestClose={close} statusBarTranslucent navigationBarTranslucent>
      <View style={StyleSheet.absoluteFill} accessibilityViewIsModal><TVPanelScrim/>
        <View pointerEvents="box-none" testID="watch-versions-tv-canvas" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
          <TVPanelSlide width={760} style={StyleSheet.absoluteFill}><FocusBoundary modal testID="watch-versions-tv" style={{position: 'absolute', right: 0, top: 0, bottom: 0, width: 760, boxShadow: '-40px 0 80px -20px rgba(0,0,0,.7)', paddingTop: 60, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}>
            <View pointerEvents="none" style={StyleSheet.absoluteFill}><Svg width="100%" height="100%" accessible={false}>
              <Defs><LinearGradient id="watch-versions-panel" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#132836"/><Stop offset="0.6" stopColor="#0d1c27"/><Stop offset="1" stopColor="#0b1822"/></LinearGradient></Defs>
              <Rect width="100%" height="100%" fill="url(#watch-versions-panel)"/>
            </Svg></View>
            <Text style={[nativeType({size: 44, weight: 800, tracking: -.03, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{title}</Text>
            <Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.text, marginTop: 10, marginBottom: 20}]}>{name}</Text>
            <View style={{flex: 1, marginHorizontal: -24, paddingHorizontal: 24}}>{body}</View>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>{key(t('title.ok'))}<Text style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('action.play')}</Text>
              <View style={{width: 16}}/><View style={{width: 16}}/>{key(t('action.back'))}<Text style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('action.close')}</Text></View>
          </FocusBoundary></TVPanelSlide>
        </View>
      </View>
    </Modal>;
  }
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={paintPermit} allowedTypes={[BottomSheet, React.Fragment, View, Text, ScrollView, Focusable, Svg, Defs, LinearGradient, Rect, Stop]}><BottomSheet open fitContent title={title} testID="watch-versions-phone" showClose={false} scrollable={false} initialFocusRef={done} returnFocusRef={value.anchorRef}
    onOpenChange={open => {if (!open) close();}} headerContent={<View style={{flexDirection: 'row', alignItems: 'flex-start', paddingHorizontal: 20, paddingTop: 2, paddingBottom: 12}}>
      <View style={{flex: 1}}><Text style={[nativeType({size: 21, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{title}</Text>
        <Text numberOfLines={1} style={[nativeType({size: 14, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 3}]}>{name}</Text></View>
      <Focusable ref={done} focusId="watch-versions-done" testID="watch-versions-done" onPress={close} style={{paddingLeft: 12, marginTop: 2}}>
        <Text style={[nativeType({size: 15, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.overlays.done')}</Text></Focusable>
    </View>} contentStyle={{paddingHorizontal: 0, paddingTop: 0}}>{body}</BottomSheet></RetainedOverlay>;
}
