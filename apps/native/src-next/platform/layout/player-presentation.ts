import type {PlatformFold, PlatformPostureSnapshot} from '../platformPostureStore';

export type PlayerRect = Readonly<{x: number; y: number; width: number; height: number}>;
/** foldable9: one layout cross-fade, omitted entirely with reduced motion. */
export const playerPostureCrossFadeMs = 250;
/** foldable5: the frame's own 276 × 234 box, plus our own 20 pt heading where
 * the frame draws a 13 pt one. */
const TABLETOP_COLUMN_EPISODES = 238;
/** foldable4's stacked half: the frame's 12+44+12+44+9+80+10+44 steps with a
 * 44 pt seek target, then the heading and two compact 66 pt rows with their 10 pt
 * gap. Both must fit before the stack is offered, so nothing is ever clipped. */
const TABLETOP_STACK_CONTROLS = 255;
const TABLETOP_STACK_EPISODES = 162;
export type PlayerWindow = Readonly<{width: number; height: number}>;
/** Geometry consumed from the posture publisher, never inferred from a device name. */
export type PlayerFold = Pick<PlatformFold, 'bounds' | 'orientation' | 'state'>;
export type PlayerPresentationInput = Readonly<{
  window: PlayerWindow;
  /** Only a report for the current window can reserve a hinge. */
  posture: Readonly<Pick<PlatformPostureSnapshot, 'status' | 'window'> & {folds: readonly PlayerFold[]}>;
  docked: boolean;
  panelOpen: boolean;
  /** foldable5: this session has episodes that follow it, so the tabletop can draw them. */
  upNext?: boolean;
}>;
export type PlayerPresentation = Readonly<{
  mode: 'docked' | 'full-window' | 'watch' | 'book' | 'tabletop';
  /** A docked surface continues to use its measured art slot, not a window-derived slot. */
  surface?: PlayerRect;
  picture?: PlayerRect;
  controls?: PlayerRect;
  content?: PlayerRect;
  details?: PlayerRect;
  /** foldable5: the following episodes beside the tabletop's controls column. */
  episodes?: PlayerRect;
  panel?: PlayerRect;
  /** foldable3d/3g: the panel is a physical page, so its edge is the crease. */
  panelPaged?: boolean;
  hingeBand?: PlayerRect;
  hideNavigation: boolean;
  hideStatusBar: boolean;
  controlsAutoHide: boolean;
  /** foldable3d keeps transport chrome hidden while its side panel is open. */
  controlsHidden: boolean;
}>;

const positive = (value: number) => Number.isFinite(value) && value > 0;
const rect = (x: number, y: number, width: number, height: number): PlayerRect => ({x, y, width, height});

/** tablet-play1/2: aspect-fit the picture; controls occupy the whole picture pane. */
export function letterboxPlayerPicture(pane: PlayerRect): PlayerRect {
  const width = Math.min(pane.width, pane.height * 16 / 9), height = width * 9 / 16;
  return rect(pane.x + (pane.width - width) / 2, pane.y + (pane.height - height) / 2, width, height);
}

function currentFold({window, posture}: PlayerPresentationInput): PlayerFold | undefined {
  if (posture.status !== 'available' || posture.window?.width !== window.width || posture.window.height !== window.height) return;
  const valid = posture.folds.filter(fold => {
    const b = fold.bounds;
    if (![b.left, b.top, b.right, b.bottom].every(Number.isFinite) || b.right < b.left || b.bottom < b.top) return false;
    // A hinge partitions the window only when both pages have positive space.
    return fold.orientation === 'vertical'
      ? b.top <= 0 && b.bottom >= window.height && b.left > 0 && b.right < window.width
      : b.left <= 0 && b.right >= window.width && b.top > 0 && b.bottom < window.height;
  });
  // Do not guess which hinge to use on an ambiguous multi-feature report.
  return valid.length === 1 ? valid[0] : undefined;
}

/** Q7 is presentation only. This function cannot navigate, mutate watch state or command an engine. */
export function resolvePlayerPresentation(input: PlayerPresentationInput): PlayerPresentation | undefined {
  const {window: {width, height}, docked, panelOpen} = input;
  if (!positive(width) || !positive(height)) return;
  if (docked) return {mode: 'docked', hideNavigation: false, hideStatusBar: false, controlsAutoHide: false, controlsHidden: false};
  const fold = currentFold(input), whole = rect(0, 0, width, height);
  const common = {hideNavigation: true, hideStatusBar: false, controlsAutoHide: true, controlsHidden: false};
  if (fold?.state === 'half-opened') {
    const b = fold.bounds;
    // foldable9: the physical fold plus16pt on each side is reserved in a half fold.
    if (fold.orientation === 'vertical') {
      const left = Math.max(0, b.left - 16), right = Math.min(width, b.right + 16);
      // foldable3g keeps the picture at the physical page width (445pt on
      // the Duo); .bk insets its controls/seek line16pt from the crease.
      const surface = rect(0, 0, b.left, Math.min(height, b.left * 9 / 16)), content = rect(right, 0, width - right, height);
      return {...common, mode: 'book', surface, picture: surface, controls: rect(0, 0, left, surface.height), content,
        details: rect(0, surface.height, left, height - surface.height),
        panel: panelOpen ? rect(b.right, 0, width - b.right, height) : undefined, panelPaged: panelOpen ? true : undefined,
        hingeBand: rect(left, 0, right - left, height)};
    }
    const top = Math.max(0, b.top - 16), bottom = Math.min(height, b.bottom + 16), lower = Math.max(0, height - bottom);
    const surface = rect(0, 0, width, top);
    // foldable5: at the drawn 880 a show's lower half holds the controls on the
    // left (508 wide, 16 under the crease) and the episodes that follow on the
    // right, in the frame's 276 × 234 box. foldable4's narrower half stacks the
    // same controls over two of frame 5's compact 66 pt rows. A film keeps the
    // one-column controls.
    const wide = width >= 880 && lower >= 240, stack = !wide && lower >= TABLETOP_STACK_CONTROLS + TABLETOP_STACK_EPISODES;
    const split = input.upNext === true && (wide || stack);
    const controls = wide && split ? rect(28, bottom, 508, Math.max(0, lower - 34))
      : stack && split ? rect(0, bottom, width, TABLETOP_STACK_CONTROLS)
      : rect(0, bottom, width, lower);
    const episodes = wide && split ? rect(576, bottom + 4, Math.max(0, width - 604), Math.min(TABLETOP_COLUMN_EPISODES, Math.max(0, lower - 4)))
      : stack && split ? rect(0, bottom + TABLETOP_STACK_CONTROLS, width, TABLETOP_STACK_EPISODES)
      : undefined;
    return {...common, mode: 'tabletop', surface, picture: letterboxPlayerPicture(surface), controls, content: controls, episodes,
      panel: panelOpen ? controls : undefined, hingeBand: rect(0, top, width, bottom - top), controlsAutoHide: false};
  }
  if (width <= height) {
    const surface = rect(0, 0, width, Math.min(height, width * 9 / 16));
    const sheet = rect(0, surface.height, width, height - surface.height);
    // phone-player 3/4: upright, the options sheet stops under the picture so a
    // subtitle change shows as it is made; the picture keeps playing behind it.
    return {...common, mode: 'watch', surface, picture: surface, controls: panelOpen ? undefined : surface,
      content: sheet, ...(panelOpen ? {panel: sheet} : {})};
  }
  if (panelOpen && fold?.orientation === 'vertical') {
    // Q7: the right page exactly, so the divider is the crease; a flat crease does
    // not split the unpanelled player. The picture keeps playing on the left page.
    const page = rect(0, 0, fold.bounds.left, height);
    return {...common, mode: 'full-window', surface: page, picture: letterboxPlayerPicture(page), hideStatusBar: true, controlsHidden: true, panelPaged: true,
      panel: rect(fold.bounds.right, 0, width - fold.bounds.right, height)};
  }
  if (panelOpen && width < 900) {
    // phone-player3: the panel floats 52 in from the right edge, 12 top and bottom,
    // over the picture that keeps playing across the whole window. It is not a
    // column: the surface is never re-cropped to the panel's left.
    const panelWidth = Math.min(356, Math.max(0, width - 52));
    return {...common, mode: 'full-window', surface: whole, picture: letterboxPlayerPicture(whole), hideStatusBar: true, controlsHidden: true,
      panel: rect(width - 52 - panelWidth, 12, panelWidth, Math.max(0, height - 24))};
  }
  if (panelOpen) {
    // Q7/tablet-play2: the unhinged full-window panel is380pt, the picture beside it.
    if (width <= 380) return;
    const surface = rect(0, 0, width - 380, height);
    return {...common, mode: 'full-window', surface, picture: letterboxPlayerPicture(surface), hideStatusBar: true, controlsHidden: true,
      panel: rect(width - 380, 0, 380, height)};
  }
  return {...common, mode: 'full-window', surface: whole, picture: letterboxPlayerPicture(whole), controls: whole, hideStatusBar: true};
}

function geometryKey(input: PlayerPresentationInput): string {
  const fold = currentFold(input);
  const b = fold?.bounds;
  return JSON.stringify([input.window.width, input.window.height, fold?.orientation, fold?.state, b?.left, b?.top, b?.right, b?.bottom]);
}

/** notes/foldable9/3d: reveal for3s after geometry changes, except under an open full-window panel. */
export function playerControlsRevealDeadline(previous: PlayerPresentationInput | undefined, next: PlayerPresentationInput, now: number, deadline = 0): number {
  const presentation = resolvePlayerPresentation(next);
  if (!Number.isFinite(now) || !presentation) return deadline;
  if (next.docked || presentation.controlsHidden) return 0;
  return previous && geometryKey(previous) !== geometryKey(next) ? now + 3000 : deadline;
}
