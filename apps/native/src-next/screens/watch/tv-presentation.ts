import {shellLayout, type ShellLayout} from '../../shell/model';
import {letterboxPlayerPicture, type PlayerPresentation, type PlayerRect, type PlayerWindow} from '../../platform/layout/player-presentation';
import type {NativeWatchPanel} from './controls';

export type NativeWatchTVPresentation = Readonly<{
  canvas: ShellLayout;
  layout: PlayerPresentation;
  panel?: PlayerRect;
  backdropRight?: number;
}>;

/** TV4 overlays the whole picture; it does not use Q7's380pt split pane.
 * Only chrome is drawn in the shell's1920x1080 canvas. The measured native
 * surface remains in real RN window coordinates, including letterbox space. */
export function nativeWatchTVPresentation(window: PlayerWindow, docked: boolean, panel?: NativeWatchPanel): NativeWatchTVPresentation | undefined {
  if (![window.width, window.height].every(value => Number.isFinite(value) && value > 0)) return;
  const canvas = shellLayout(window.width, window.height, true);
  if (docked) return {canvas, layout: {mode: 'docked', hideNavigation: false, hideStatusBar: false, controlsAutoHide: false, controlsHidden: false}};
  const surface = {x: 0, y: 0, width: window.width, height: window.height};
  // playback-info.html .tinfo is960; title8b .tchoose is a separate760 surface.
  const width = panel === 'playback-info' ? 960 : panel && panel !== 'episodes' ? 680 : undefined;
  const logical = width ? {x: canvas.width - width, y: 0, width, height: canvas.height} : undefined;
  const physical = logical ? {x: canvas.left + logical.x * canvas.scale, y: canvas.top, width: logical.width * canvas.scale, height: logical.height * canvas.scale} : undefined;
  return {canvas, panel: logical, backdropRight: width ? panel === 'playback-info' ? 1056 : 780 : undefined,
    layout: {mode: 'full-window', surface, picture: letterboxPlayerPicture(surface), controls: surface, panel: physical,
      hideNavigation: true, hideStatusBar: true, controlsAutoHide: true, controlsHidden: false}};
}
