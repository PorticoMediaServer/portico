/** Shared SVG geometry. Renderers own color, size and accessible button labels. */
export type MediaFamily = 'movie' | 'tv' | 'anime' | 'audiobook' | 'music';
export type MediaIconName = 'film' | 'tv' | 'book' | 'music';
export type IconPrimitive = Readonly<
  | { type: 'path'; d: string }
  | { type: 'rect'; x: number; y: number; width: number; height: number; rx: number }
  | { type: 'ellipse'; cx: number; cy: number; rx: number; ry: number }
>;
export type MediaIconDefinition = Readonly<{
  viewBox: '0 0 24 24'; fill: 'none'; strokeWidth: number;
  strokeLinecap: 'round'; strokeLinejoin: 'round';
  primitives: readonly IconPrimitive[];
}>;
const definition = (primitives: IconPrimitive[]): MediaIconDefinition => Object.freeze({
  viewBox: '0 0 24 24', fill: 'none', strokeWidth: 1.65,
  strokeLinecap: 'round', strokeLinejoin: 'round',
  primitives: Object.freeze(primitives.map(primitive => Object.freeze(primitive))),
});
/** Adapted from the existing web family icons; native uses the identical primitives. */
export const mediaIcons: Readonly<Record<MediaIconName, MediaIconDefinition>> = Object.freeze({
  film: definition([
    { type: 'rect', x: 3, y: 3, width: 18, height: 18, rx: 2 },
    { type: 'path', d: 'M7 3v18M17 3v18M3 8h4m10 0h4M3 16h4m10 0h4' },
  ]),
  tv: definition([
    { type: 'rect', x: 2, y: 4, width: 20, height: 14, rx: 2 },
    { type: 'path', d: 'M8 21h8m-4-3v3' },
  ]),
  book: definition([
    { type: 'path', d: 'M12 5c-3-2-6-2-10-1v15c4-1 7-1 10 1 3-2 6-2 10-1V4c-4-1-7-1-10 1v15' },
  ]),
  music: definition([
    { type: 'path', d: 'M9 18V5l11-2v13M9 8l11-2' },
    { type: 'ellipse', cx: 6, cy: 18, rx: 3, ry: 2 },
    { type: 'ellipse', cx: 17, cy: 16, rx: 3, ry: 2 },
  ]),
});
const familyIcons: Readonly<Record<MediaFamily, MediaIconName>> = Object.freeze({
  movie: 'film', tv: 'tv', anime: 'tv', audiobook: 'book', music: 'music',
});
/** Unknown kinds are explicit: callers must not silently relabel them as movies. */
export function mediaFamilyIcon(kind: string): MediaIconName | undefined {
  return Object.prototype.hasOwnProperty.call(familyIcons, kind) ? familyIcons[kind as MediaFamily] : undefined;
}
