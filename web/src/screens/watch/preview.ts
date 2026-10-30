import type {CSSProperties} from 'react';
import type {TrickplayFrame} from '@core/trickplay.ts';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {WatchTrickplay} from './types';
import {watchSeekBounds} from './model';

/** No resource interest can escape the supplied current-source selection. */
export function watchPreviewFrame(snapshot: WebPlaybackRuntimeSnapshot, port: WatchTrickplay | undefined, seconds: number | null) {
  const bounds = watchSeekBounds(snapshot);
  return !snapshot.disposed && snapshot.dock.remote?.phase !== 'playing' && snapshot.fence?.kind === 'queue'
    && bounds && seconds !== null && Number.isFinite(seconds) && seconds >= bounds.start && seconds <= bounds.end
    && port && sameFence(snapshot.fence, port.fence) && port.isCurrent() ? port.frameAt(seconds) : null;
}

/** Crop only the real parsed frame from the parent's protected sprite image. */
export function scrubImageStyle(frame: TrickplayFrame, sheet: {width?: number; height?: number}, phone: boolean): CSSProperties | null {
  const {width, height} = sheet;
  if (![frame.x, frame.y, frame.width, frame.height, width, height].every(value => typeof value === 'number' && Number.isFinite(value))
    || !width || !height || frame.x < 0 || frame.y < 0 || frame.width <= 0 || frame.height <= 0
    || frame.x + frame.width > width || frame.y + frame.height > height) return null;
  const boxWidth = phone ? 178 : 256, boxHeight = phone ? 100 : 144;
  const scale = Math.max(boxWidth / frame.width, boxHeight / frame.height);
  return {position: 'absolute', width: width * scale, height: height * scale,
    left: (boxWidth - frame.width * scale) / 2 - frame.x * scale,
    top: (boxHeight - frame.height * scale) / 2 - frame.y * scale};
}
