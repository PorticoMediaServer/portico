export type GallerySearch = {family?: string; case?: string; capture?: boolean; width?: number; height?: number};
export function gallerySearch(raw: Record<string, unknown>): GallerySearch {
  const id = (value: unknown) => typeof value === 'string' && /^[a-z0-9][a-z0-9-]{0,79}$/.test(value) ? value : undefined;
  const size = (value: unknown) => { const n = Number(value); return Number.isInteger(n) && n >= 1 && n <= 7680 ? n : undefined; };
  return {family: id(raw.family), case: id(raw.case), capture: raw.capture === true || raw.capture === 'true', width: size(raw.width), height: size(raw.height)};
}
