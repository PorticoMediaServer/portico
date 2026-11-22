import React, {useEffect, useRef, useSyncExternalStore} from 'react';
import {Modal, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {shellLayout} from '../../shell/model';
import {Focusable} from '../controls';
import {BottomSheet} from '../overlays';
import {StateView} from '../states';
import {overlayPaintKey, RetainedOverlay, type OverlayPaintPermit} from '../overlays/SurfacePaintHost';
import {FocusBoundary} from '../overlays/surface';
import {TVPanelSlide, TVPanelScrim, useTVPanelInteractive, useTVPanelModal} from '../overlays/TVPanelMotion';
import {useOverlayLifecycle} from '../overlays/OverlayHost';
import {Icon} from '../Icon';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {textLines, useTheme} from '../theme';
import {shellType} from '../shell/visual';
import type {NativeDiscTitlesController} from '../../screens/watch/disc-titles-controller';

/** playback-info frame 20: the disc's own titles as one side sheet. Focus enters
 * on the title playing now, OK makes another the main title and the server then
 * analyzes it. These rows are the scan's published titles, never alternate
 * versions of the film, so nothing here offers a different file to play. The
 * server's per-list and per-row ready flags are shown as pending preparation, so
 * a freshly chosen title never looks settled before its analysis is done. */
export function NativeDiscTitlesSheet({controller, subject, close, renderError, paintPermit}: {
  paintPermit?: OverlayPaintPermit;
  controller: NativeDiscTitlesController;
  subject: string;
  close: () => void;
  renderError: (error: unknown, retry: () => void) => React.ReactNode;
}) {
  const theme = useTheme(), window = useWindowDimensions(), t = defaultI18n.t;
  const interactive = useTVPanelInteractive(), modal = useTVPanelModal();
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const drawn = state.titles, rows = drawn ? drawn.titles : [];
  const mainIndex = rows.findIndex(row => row.main);
  const first = useRef<View>(null);
  // One read per opening; a Retry or a stale refresh drives its own read.
  useEffect(() => {void controller.load();}, [controller]);
  useOverlayLifecycle(theme.isTV, true, close, undefined, undefined, 0, 'watch-disc-titles');
  useEffect(() => {if (theme.isTV && interactive) first.current?.requestTVFocus?.();}, [theme.isTV, interactive, mainIndex, state.phase]);
  const title = t('next.overlays.discTitles');
  const body = <ScrollView testID="watch-disc-titles-rows" contentContainerStyle={{paddingTop: theme.isTV ? 4 : 0}}>
    {!!state.error && <View style={{marginBottom: 12}}>{renderError(state.error, () => {void controller.load();})}</View>}
    {state.phase === 'loading' && <Text style={[shellType(theme.isTV ? 22 : 15, 500, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.browse.native.markAllPreparing')}</Text>}
    {/* The server's own ready flag: a chosen title whose analysis has not finished
        yet is still being prepared. Say that fact with the neutral word; no cause,
        no time estimate and no repeated write of our own. */}
    {!!drawn && !drawn.ready && <Text testID="watch-disc-titles-pending" style={[shellType(theme.isTV ? 22 : 15, 500, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary, marginBottom: 12}]}>{t('next.browse.native.markAllPreparing')}</Text>}
    {rows.map((row, index) => {
      const active = row.main, entry = active || (mainIndex < 0 && index === 0);
      const length = durationText(Math.round(row.durationMs / 1000), t);
      const rowStyle = (focused: boolean) => ({minHeight: theme.isTV ? 84 : 64, paddingHorizontal: theme.isTV ? 18 : 16, paddingVertical: theme.isTV ? 14 : 10, marginBottom: theme.isTV ? 14 : 0,
          flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.isTV ? 16 : 12, borderRadius: theme.isTV ? 12 : 0,
          backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent'});
      return <Focusable key={row.id} ref={entry ? first : undefined} focusId={`watch-disc-title-${row.id}`} testID={`watch-disc-title-${row.id}`}
        focusKind="row" focusRadius={theme.isTV ? 12 : 0} accessibilityRole="button" accessibilityState={{selected: active, busy: state.phase === 'writing' && state.writingTitleId === row.id}}
        preferredFocus={theme.isTV && interactive && entry} disabled={state.phase === 'writing'}
        onPress={() => {if (drawn) void controller.choose(row.id, drawn);}}
        style={theme.isTV ? ({focused}) => rowStyle(focused) : rowStyle(false)}>
        <View style={{flex: 1, minWidth: 0}}>
          <Text {...textLines(1)} style={[shellType(theme.isTV ? 24 : 15.5, 700, 'normal', theme.isTV ? 0 : -.01, theme.textScale),
            {color: theme.colors.text}]}>{t('next.overlays.discTitle', {number: row.number})}</Text>
          <Text {...textLines(1)} style={[shellType(theme.isTV ? 19 : 13, 500, theme.isTV ? 'normal' : 18.2, 0, theme.textScale),
            {marginTop: theme.isTV ? 2 : 3, color: theme.colors.textTertiary}]}>{[active ? t('next.overlays.discMainTitle') : undefined,
              row.ready ? t('next.overlays.discChapters', {count: row.chapterCount}) : t('next.browse.native.markAllPreparing')].filter(Boolean).join(' · ')}</Text>
        </View>
        <Text style={[shellType(theme.isTV ? 22 : 14, 600, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary}]}>{length}</Text>
        {active && <Icon name="i-check" size={theme.isTV ? 26 : 20} color={theme.colors.accentStrong}/>}
      </Focusable>;
    })}
    <Text style={[shellType(theme.isTV ? 20 : 12.5, 400, theme.isTV ? 28 : 18, 0, theme.textScale), {marginTop: theme.isTV ? 18 : 10, color: theme.colors.textTertiary}]}>{t('next.overlays.discTitlesNote')}</Text>
  </ScrollView>;
  if (!theme.isTV) return <RetainedOverlay key={overlayPaintKey(controller)} permit={paintPermit} allowedTypes={[BottomSheet, React.Fragment, View, Text, ScrollView, Focusable, Icon, StateView, Svg, Defs, LinearGradient, Rect, Stop]}><BottomSheet open fitContent title={title} testID="watch-disc-titles-phone" showClose={false} scrollable={false} initialFocusRef={first}
    onOpenChange={open => {if (!open) close();}} contentStyle={{paddingHorizontal: 0, paddingTop: 0}}>{body}</BottomSheet></RetainedOverlay>;
  const canvas = shellLayout(window.width, window.height, true);
  const key = (label: string) => <View style={{height: 36, minWidth: 44, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: '#edf5fa4d', alignItems: 'center', justifyContent: 'center'}}>
    <Text style={[shellType(17, 800, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text></View>;
  return <Modal key={modal.key} transparent visible={modal.visible} onDismiss={modal.onDismiss} onShow={modal.onShow} animationType="none" onRequestClose={close} statusBarTranslucent navigationBarTranslucent>
    <View style={StyleSheet.absoluteFill} accessibilityViewIsModal><TVPanelScrim/>
      <View pointerEvents="box-none" testID="watch-disc-titles-tv-canvas" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
        <TVPanelSlide width={760} style={StyleSheet.absoluteFill}><FocusBoundary modal testID="watch-disc-titles-tv" style={{position: 'absolute', right: 0, top: 0, bottom: 0, width: 760, boxShadow: '-40px 0 80px -20px rgba(0,0,0,.7)', paddingTop: 60, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}>
          <View pointerEvents="none" style={StyleSheet.absoluteFill}><Svg width="100%" height="100%" accessible={false}>
            <Defs><LinearGradient id="watch-disc-titles-panel" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#132836"/><Stop offset="0.6" stopColor="#0d1c27"/><Stop offset="1" stopColor="#0b1822"/></LinearGradient></Defs>
            <Rect width="100%" height="100%" fill="url(#watch-disc-titles-panel)"/>
          </Svg></View>
          <Text style={[shellType(44, 800, 'normal', -.03, theme.textScale), {color: theme.colors.text}]}>{title}</Text>
          <Text {...textLines(2)} style={[shellType(22, 700, 31.9, 0, theme.textScale), {color: theme.colors.text, marginTop: 10, marginBottom: 20}]}>{subject}</Text>
          <View style={{flex: 1, marginHorizontal: -24, paddingHorizontal: 24}}>{body}</View>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>{key(t('title.ok'))}<Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.overlays.discMakeMain')}</Text>
            <View style={{width: 16}}/><View style={{width: 16}}/>{key(t('action.back'))}<Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('action.close')}</Text></View>
        </FocusBoundary></TVPanelSlide>
      </View>
    </View>
  </Modal>;
}
