import type {ArtworkSource} from './types';

/**
 * What owns one mounted picture. The request's paint order, priority, batching group and large
 * flag are hints for *when* an image draws, never *which* image it is. A Home row that gains or
 * loses a card, or a row that moves in the document, hands every card after it a new position;
 * that must keep the artwork already on screen rather than retire it and mount a blank node
 * until the replacement decodes. The scheduler, the exact resource key and the retention owner
 * are the picture's identity, together with whether it is visible at all.
 */
export type ArtworkIdentity = Readonly<{
  scheduler: ArtworkSource['scheduler'] | undefined;
  key: string | undefined;
  /** The runtime's frozen retention owner, or a custom source's own retain callback. */
  retention: unknown;
  visible: boolean;
}>;

/** The identity fields alone, as primitives a caller can memoise on. */
export function artworkIdentityParts(source: ArtworkSource | null | undefined): Omit<ArtworkIdentity, 'visible'> {
  return {scheduler: source?.scheduler, key: source?.key, retention: source?.retainScope ?? source?.retain};
}

export function artworkIdentity(source: ArtworkSource | null | undefined, visible: boolean): ArtworkIdentity {
  return {...artworkIdentityParts(source), visible};
}

export function sameArtworkIdentity(left: ArtworkIdentity | undefined, right: ArtworkIdentity): boolean {
  return !!left && left.scheduler === right.scheduler && left.key === right.key && left.retention === right.retention && left.visible === right.visible;
}
