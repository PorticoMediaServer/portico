/**
 * Stable width hints for callers of this helper. The server maps requested
 * widths onto its small and large artwork slots; encoded dimensions follow
 * metadata/artwork_encoding.go and can depend on the owner's image policy.
 *
 * Mounted surfaces use artworkRenditionFor in renditions.ts with their measured
 * pixel box. These legacy buckets preserve their request and cache keys.
 */
export const artworkSizeBuckets: readonly number[] = [400, 800, 1920, 2560];

export type ArtworkBucketOptions = Readonly<{
  /** Rendered width ÷ height (poster 2/3, landscape 16/9, square 1). Default 1. */
  aspect?: number;
  /** Request width hints, ascending. Default `artworkSizeBuckets`. */
  buckets?: readonly number[];
  /** A width hint may be up to this much smaller than the rendered pixels (default 1.1 = 10%). */
  tolerance?: number;
}>;

/**
 * The width hint to request for artwork rendered `devicePixelWidth` wide
 * (CSS width × devicePixelRatio, or points × screen scale).
 */
export function artworkWidthBucket(devicePixelWidth: number, options: ArtworkBucketOptions = {}): number {
  const buckets = options.buckets?.length ? options.buckets : artworkSizeBuckets;
  const aspect = options.aspect && options.aspect > 0 && Number.isFinite(options.aspect) ? options.aspect : 1;
  if (!(devicePixelWidth > 0) || !Number.isFinite(devicePixelWidth)) return buckets[0]!;
  const longEdge = aspect < 1 ? devicePixelWidth / aspect : devicePixelWidth;
  const needed = longEdge / Math.max(1, options.tolerance ?? 1.1);
  for (const bucket of buckets) if (bucket >= needed) return bucket;
  return buckets[buckets.length - 1]!;
}
