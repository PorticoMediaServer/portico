/**
 * MU4 CON-26: one Quality model on both platforms. Pure and platform-free.
 *
 * "Automatic" comes first, then the rungs; the footer reads "Home network ·
 * up to 1080p" / "Away from home · up to 1080p" from the same delivery
 * policy on both. Same input, same output, whichever app renders it.
 */
export type QualityRungLike = Readonly<{id: string; kind: string; label: string; enabled: boolean; reason?: string; targetDisplayHeight?: number; maxVideoBitrateBps?: number; maxAudioBitrateBps?: number}>;
export type QualitySourceLike = Readonly<{qualities: readonly QualityRungLike[]}>;
export type DeliveryPolicyLike = Readonly<{networkClass: 'local' | 'remote' | 'unknown' | string; maxVideoHeight?: unknown} | null | undefined>;

/** The server's ladder rungs across sources, deduplicated by id (order kept). */
export function dedupQualityRungs(sources: readonly QualitySourceLike[] | null | undefined): QualityRungLike[] {
  if (!Array.isArray(sources)) return [];
  const out: QualityRungLike[] = [];
  const seen = new Set<string>();
  for (const src of sources) {
    if (!src || !Array.isArray(src.qualities)) continue;
    for (const q of src.qualities) {
      if (!q || seen.has(q.id)) continue;
      seen.add(q.id);
      out.push(q);
    }
  }
  return out;
}

/** The "Convert" rungs: everything past Automatic/Original. */
export function convertQualityRungs(rungs: readonly QualityRungLike[] | null | undefined): QualityRungLike[] {
  if (!Array.isArray(rungs)) return [];
  return rungs.filter(q => q && q.kind !== 'automatic' && q.kind !== 'original');
}

export type QualityNetwork = 'home' | 'away' | null;
export type QualityFooter = Readonly<{network: QualityNetwork; height: number | null}>;

/** Footer parts from the resolved delivery policy (null policy reads as no footer). */
export function qualityNetworkFooter(policy: DeliveryPolicyLike): QualityFooter {
  if (!policy || typeof policy !== 'object') return Object.freeze({network: null, height: null});
  const network: QualityNetwork =
    policy.networkClass === 'local' ? 'home'
    : policy.networkClass === 'remote' ? 'away'
    : null;
  const height = typeof policy.maxVideoHeight === 'number' && Number.isFinite(policy.maxVideoHeight) && policy.maxVideoHeight > 0 ? Math.floor(policy.maxVideoHeight) : null;
  return Object.freeze({network, height});
}

/** Whether the footer has something to say (a network and a height). */
export function hasQualityFooter(footer: QualityFooter): boolean {
  return footer.network !== null && footer.height !== null;
}
