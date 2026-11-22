import React from 'react';
import {View} from 'react-native';
import {LayerStatusBar} from '../shell/Layer';
import Animated from 'react-native-reanimated';
import type {PlayerRect} from '../../platform/layout/player-presentation';
import {TVPanelTransition, TVPanelSlide, TVPanelScrim, useTVPanelPresence} from '../overlays/TVPanelMotion';
import {NativeWatchChrome} from '../../screens/watch/WatchChrome';
// Named through kit/ so the screen harness loads it for real when a test loads the watch host.
import {PlaybackWaiting} from '../../kit/states/PlaybackWaiting';
import {NativeWatchPicture} from '../../shell/watch-picture';
import {NativeHandheldMiniGuide} from './HandheldMiniGuide';
import type {NativeWatchHostContext, NativeWatchHostBindings, WatchHostProps, useWatchHost} from '../../pages/shared/player/useWatchHost';

export type WatchHostViewProps = Pick<WatchHostProps, 'authority' | 'playback' | 'outlet' | 'runtime' | 'controls' | 'bindings' | 'isTV'> & NonNullable<ReturnType<typeof useWatchHost>>;

const positioned = (pane: PlayerRect) => ({position: 'absolute' as const, left: pane.x, top: pane.y, width: pane.width, height: pane.height});

/** The watch host's drawing: the measured picture slot, the TV canvas or the page panes (content, details, episodes,
 * controls, waiting, panel, failure, post-play) and the player's status bar, from what `useWatchHost` returns. */
export function WatchHostView({surface, authority, playback, outlet, runtime, controls, bindings, isTV, noticesEnabled, source, liveGuide, window, safeTop, fence, expanded, guideOpen, panelOpen, lossNotice, waitingBottomInset, setWaitingBottomInset, noticeKey, television, layout, fullWindow, fullWindowRequest, measuredCurrent, ink, initialPlayer, paneFadeStyle, context, chromePane, postPlay, failureAction, failed, waiting}: WatchHostViewProps) {
  // The watch page sits over the route stack, which keeps its own pages (the title page that started playback, or
  // wherever the viewer went). Its panes are opaque ink, as the shell is under its routes, so no page shows through (W3 F7, F8).
  const pagePane = (rect: Parameters<typeof positioned>[0]) => isTV ? positioned(rect) : {...positioned(rect), backgroundColor: ink};
  const content = <Animated.View testID="watch-panes" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted}
    importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', inset: 0}, paneFadeStyle]}>
    {postPlay ? <View testID="watch-post-play-pane" style={{position: 'absolute', inset: 0}}>{postPlay}</View> : <>
    {layout.content && <View testID="watch-content-pane" style={pagePane(layout.content)} pointerEvents="box-none">{bindings.content?.(context)}</View>}
    {layout.details && <View testID="watch-details-pane" style={pagePane(layout.details)} pointerEvents="box-none">{bindings.details?.(context)}</View>}
    {layout.episodes && <View testID="watch-episodes-pane" style={pagePane(layout.episodes)} pointerEvents="box-none">{bindings.episodes?.(context)}</View>}
    {chromePane && <View testID="watch-controls-pane" pointerEvents="box-none" style={positioned(television ? {x: 0, y: 0, width: television.canvas.width, height: television.canvas.height} : chromePane)}>
      <NativeWatchChrome {...bindings.chrome(source, runtime)} waiting={waiting.waiting} onPictureControlsLayout={setWaitingBottomInset} notice={(noticesEnabled || lossNotice?.value.required) && expanded && lossNotice && lossNotice.key === noticeKey && layout.mode !== 'watch' && layout.mode !== 'book' ? {key: lossNotice.key, value: lossNotice.value, shownAt: 0} : undefined} source={source} controls={controls} playback={playback} text={runtime.text}
        width={television?.canvas.width ?? chromePane.width} height={television?.canvas.height ?? chromePane.height} compact={layout.mode === 'watch' || layout.mode === 'book'} fullWindowRequest={fullWindowRequest} tablet={!isTV && Math.min(window.width, window.height) >= 600}
        tabletop={!isTV && layout.mode === 'tabletop' && layout.episodes && layout.controls ? (layout.controls.width < window.width ? 'column' : 'stacked') : undefined}
        bar={!isTV && layout.mode === 'full-window' && window.width >= 1024}
        tvPanelRight={television?.backdropRight} failureAction={failureAction}
        edgeInsets={layout.mode === 'book' && layout.controls ? {right: surface.width - layout.controls.width} : layout.mode === 'watch' && !isTV && safeTop > 0 ? {top: safeTop} : undefined}/>
    </View>}
    {/* Over the picture and the controls’ gradient, below panels: "Getting ready" while the server converts the stream, or the connection
        line while media stops arriving (definition 07, rule 3; the web player's same rule). */}
    <View testID="watch-waiting-pane" pointerEvents="none" style={positioned(television ? {x: 0, y: 0, width: television.canvas.width, height: television.canvas.height} : surface)}>
      <PlaybackWaiting {...waiting} bottomInset={chromePane === surface ? waitingBottomInset : 0}/>
    </View>
    {isTV ? <TVWatchPanel context={context} render={bindings.panel}/> : panelOpen && <View testID="watch-panel-pane" pointerEvents="box-none" style={context.renderer.panel ? positioned(context.renderer.panel) : {position: 'absolute', inset: 0}}>
      {guideOpen && bindings.liveGuide ? <NativeHandheldMiniGuide key={JSON.stringify(fence)} context={context} guide={bindings.liveGuide} snapshot={liveGuide}/> : bindings.panel(context)}
    </View>}
    {failed && <View testID="watch-failed-pane" pointerEvents="box-none" style={isTV ? {position: 'absolute', inset: 0} : positioned(surface)}>{failed}</View>}
    </>}
  </Animated.View>;
  return <View testID="watch-host" pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>
    <NativeWatchPicture authority={authority} fence={fence} outlet={outlet} current={measuredCurrent} style={positioned(surface)}/>
    {television ? <View testID="watch-tv-canvas" pointerEvents="box-none" style={{position: 'absolute', left: television.canvas.left, top: television.canvas.top,
      width: television.canvas.width, height: television.canvas.height, transformOrigin: 'top left', transform: [{scale: television.canvas.scale}]}}>{content}</View> : content}
    {fullWindow && !isTV && <LayerStatusBar kind="player"/>}
  </View>;
}

/** The live controls state closes immediately; only inert outgoing paint is retained. */
function TVWatchPanel({context, render}: {context: NativeWatchHostContext; render: NativeWatchHostBindings['panel']}) {
  const presence = useTVPanelPresence(context.state.panel ? context : undefined, JSON.stringify(context.fence));
  const shown = presence.value;
  if (!shown) return null;
  const pane = shown.renderer.panel;
  return <TVPanelTransition open={presence.open} onExited={() => {presence.exited(); context.panelExited();}}>
    <TVPanelScrim/>
    <View testID="watch-panel-pane" pointerEvents="box-none" style={pane ? positioned(pane) : {position: 'absolute', inset: 0}}>
      <TVPanelSlide width={pane?.width ?? shown.renderer.width}>{render(shown)}</TVPanelSlide>
    </View>
  </TVPanelTransition>;
}
