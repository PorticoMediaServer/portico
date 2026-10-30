import type {ArtworkPosition, ArtworkSource, CoverShape} from './types';

export const unit = (value: number): number => Math.min(1, Math.max(0, Number.isFinite(value) ? value : 0.5));

/** A caller's picture timing cannot override system/app reduction or cached immediacy. */
export function artworkArrivalDuration(requested: number | undefined, standard: number, reduced: boolean, cached: boolean): number {
  return cached ? 0 : reduced ? 160 : requested !== undefined && Number.isFinite(requested) ? Math.max(0, requested) : standard;
}

/** Crop in the rendered box, independent of transport or item identity. */
export function artworkFrame(width: number, height: number, intrinsicWidth: number, intrinsicHeight: number, fit: 'cover' | 'contain', position: ArtworkPosition = {x: 0.5, y: 0.5}, zoom = 1) {
  const scale = (fit === 'contain' ? Math.min : Math.max)(width / intrinsicWidth, height / intrinsicHeight) * Math.max(1, Number.isFinite(zoom) ? zoom : 1);
  const w = intrinsicWidth * scale, h = intrinsicHeight * scale;
  const left = (width - w) * unit(position.x), top = (height - h) * unit(position.y);
  return {width: w, height: h, left: left === 0 ? 0 : left, top: top === 0 ? 0 : top};
}

/** Headers stay on the native Image; scheduler identity never becomes a request URL. A display
 * size (painted points and screen scale) lets the platform decode at that size, not the file's. */
export function artworkAttemptSource(source: ArtworkSource, uri: string, attempt: number, display?: {width: number; height: number; scale: number}) {
  return {
    uri: attempt > 0 ? `${uri}${uri.includes('?') ? '&' : '?'}_artRetry=${attempt}` : uri,
    ...(source.headers ? {headers: {...source.headers}} : {}),
    ...(display && display.width > 0 && display.height > 0 && display.scale > 0 ? {width: Math.round(display.width), height: Math.round(display.height), scale: display.scale} : {}),
  };
}

export function boundedCovers(covers: readonly ArtworkSource[], maximum: number): readonly ArtworkSource[] {
  return covers.slice(0, maximum);
}
export const coverAspect = (shape: CoverShape): number => shape === 'square' ? 1 : shape === 'periodical' ? 3 / 4 : 2 / 3;

/** Newest-first supplied slots: two put newest right; three use newest in the full left half. */
export function circleArtworkFrames(count: number, size: number, gap: number) {
  const half = (size - gap) / 2;
  return Array.from({length: Math.min(4, Math.max(0, count))}, (_, index) => {
    if (count === 1) return {left: 0, top: 0, width: size, height: size};
    if (count === 2) return {left: index === 0 ? half + gap : 0, top: 0, width: half, height: size};
    if (count === 3) return {left: index === 0 ? 0 : half + gap, top: index === 2 ? half + gap : 0, width: half, height: index === 0 ? size : half};
    return {left: index % 2 ? half + gap : 0, top: index > 1 ? half + gap : 0, width: half, height: half};
  });
}

export function mixColour(first: string, second: string, fraction: number): string {
  if (!/^#[a-f\d]{6}$/i.test(first) || !/^#[a-f\d]{6}$/i.test(second)) return first;
  const weight = unit(fraction);
  return '#' + [1, 3, 5].map(i => Math.round(parseInt(first.slice(i, i + 2), 16) * (1 - weight) + parseInt(second.slice(i, i + 2), 16) * weight).toString(16).padStart(2, '0')).join('');
}
