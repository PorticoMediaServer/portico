/** Discrete, settled posture from the platform; browser absence means ordinary window shape. */
export type PlaybackWindow = Readonly<{
  width: number; height: number; posture: 'closed' | 'flat' | 'book' | 'tabletop';
  hinge?: Readonly<{axis: 'vertical' | 'horizontal'; x: number; y: number; width: number; height: number}>;
}>;
export type VideoPresentationLayout = Readonly<{
  mode: 'docked' | 'full-window' | 'watch' | 'book' | 'tabletop';
  hideStatusAndRail: boolean; panelWidth: number;
  picture: Readonly<{x: number; y: number; width: number; height: number}>;
  hingeBand?: Readonly<{x: number; y: number; width: number; height: number}>;
}>;
/** Ruling Q7. Pure geometry, never navigation, engine commands or a new media element. */
export function videoPresentationLayout(window: PlaybackWindow, docked: boolean): VideoPresentationLayout {
  const {width, height, hinge} = window;
  if (![width, height].every(n => Number.isFinite(n) && n > 0)) throw new Error('invalid_playback_window');
  if (docked) return {mode: 'docked', hideStatusAndRail: false, panelWidth: 0, picture: {x: 0, y: 0, width: 0, height: 0}};
  if (hinge && (![hinge.x, hinge.y, hinge.width, hinge.height].every(n => Number.isFinite(n) && n >= 0) || hinge.x + hinge.width > width || hinge.y + hinge.height > height)) throw new Error('invalid_playback_hinge');
  const vertical = hinge?.axis === 'vertical' ? hinge : undefined;
  if (window.posture === 'book' && !vertical || window.posture === 'tabletop' && hinge?.axis !== 'horizontal') throw new Error('playback_hinge_unavailable');
  const panelWidth = vertical ? width - vertical.x - vertical.width : Math.min(380, width);
  if (window.posture === 'book' && vertical) {
    const band = {x: Math.max(0, vertical.x - 16), y: 0, width: vertical.width + 32, height};
    const pictureWidth = band.x;
    return {mode: 'book', hideStatusAndRail: false, panelWidth, hingeBand: band, picture: {x: 0, y: 0, width: pictureWidth, height: Math.min(pictureWidth * 9 / 16, height)}};
  }
  if (window.posture === 'tabletop' && hinge?.axis === 'horizontal') {
    const top = Math.max(0, hinge.y - 16), pictureHeight = Math.min(top, width * 9 / 16);
    return {mode: 'tabletop', hideStatusAndRail: false, panelWidth, hingeBand: {x: 0, y: top, width, height: hinge.height + 32}, picture: {x: 0, y: (top - pictureHeight) / 2, width, height: pictureHeight}};
  }
  if (width > height) {
    const pictureWidth = Math.min(width, height * 16 / 9), pictureHeight = pictureWidth * 9 / 16;
    return {mode: 'full-window', hideStatusAndRail: true, panelWidth, picture: {x: (width - pictureWidth) / 2, y: (height - pictureHeight) / 2, width: pictureWidth, height: pictureHeight}};
  }
  return {mode: 'watch', hideStatusAndRail: false, panelWidth, picture: {x: 0, y: 0, width, height: width * 9 / 16}};
}

/** One settled change, one fade/reveal. Watch route/tab/season/scroll stay with their owner. */
export class VideoPresentationContinuity {
  private key?: string;
  apply(window: PlaybackWindow, docked: boolean, reducedMotion: boolean,
    effects: Readonly<{layout(value: VideoPresentationLayout): void; crossfade(ms: number): void; revealControls(ms: number): void}>): void {
    const h = window.hinge;
    const key = JSON.stringify([window.width, window.height, window.posture, h?.axis, h?.x, h?.y, h?.width, h?.height, docked]);
    if (key === this.key) return;
    const changed = this.key !== undefined;
    effects.layout(videoPresentationLayout(window, docked));
    this.key = key;
    if (changed) {
      effects.crossfade(reducedMotion ? 0 : 250);
      if (!docked) effects.revealControls(3000);
    }
  }
}
