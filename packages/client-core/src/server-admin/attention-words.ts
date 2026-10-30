/**
 * The needs-attention list in words, once for every client: why a title is on it, why a
 * suggested match was suggested, and how many titles are waiting for the owner. The reasons are
 * the server's codes; one this app has no words for reads as the general sentence, never as the
 * code.
 */
import type {AttentionCandidate, AttentionEntry, AttentionPage} from '../metadata-attention.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;
type Has = (id: string) => boolean;

/** Work the server is still doing by itself: nothing for the owner to decide yet. */
const TRANSIENT = new Set(['pending', 'searching', 'pending_search', 'pending_episodes', 'pending_children', 'pending_apply']);
export const attentionInProgress = (reason: string): boolean => TRANSIENT.has(reason);

/** Why the title is on the list, as a sentence; an album, track or book may have its own words for a reason. */
export function attentionReason(t: Translate, has: Has, reason: string, kind?: string): string {
  const own = kind ? `metadata.attention.${kind}.${reason}` : '';
  if (own && has(own)) return t(own as MessageId);
  const id = `metadata.attention.${reason}`;
  return t(has(id) ? (id as MessageId) : 'metadata.attention.other');
}

/** Why a match was suggested, as a few short facts ("Same title", "Same year"), each once; a fact this app has no words for is left out. */
export function candidateFacts(t: Translate, has: Has, candidate: Pick<AttentionCandidate, 'reasons'>): readonly string[] {
  const out: string[] = [];
  for (const reason of candidate.reasons) {
    const id = `metadata.match.${reason}`;
    if (!has(id)) continue;
    const words = t(id as MessageId);
    if (!out.includes(words)) out.push(words);
  }
  return out;
}

const PROVIDERS: Readonly<Record<string, string>> = {tmdb: 'TMDB', tvdb: 'TheTVDB', anilist: 'AniList'};
/** A provider by its own name. */
export const providerName = (provider: string): string => PROVIDERS[provider] ?? provider.toUpperCase();

/** "The Matrix (1999) · TMDB": a suggested match on one line. */
export function candidateLine(candidate: Pick<AttentionCandidate, 'title' | 'year' | 'identity'>): string {
  return `${candidate.title}${candidate.year ? ` (${candidate.year})` : ''} · ${providerName(candidate.identity.provider)}`;
}

export type AttentionCount = Readonly<{/** Titles on this page that wait for the owner (work still in progress is not counted). */ count: number; /** There are more pages, so the true number is at least this. */ more: boolean}>;

/** How many titles wait for the owner, from the first page of the list. */
export function attentionCount(page: Pick<AttentionPage, 'entries' | 'nextCursor'> & {total?:number}): AttentionCount {
  if(page.total!==undefined)return {count:page.total,more:false};
  return {count: page.entries.filter(entry => !attentionInProgress(entry.reason)).length, more: !!page.nextCursor};
}

/** The count as a badge shows it: "7", "25+", or nothing when no title waits. */
export function attentionCountText(count: AttentionCount | undefined, number: (n: number) => string): string {
  if (!count || !count.count) return '';
  return count.more ? `${number(count.count)}+` : number(count.count);
}

/** Whether the server offers this action for the title. The server decides; the list never offers one it did not name. */
export const attentionOffers = (entry: Pick<AttentionEntry, 'actions'>, action: 'accept' | 'search' | 'id' | 'dismiss' | 'parent' | 'retry'): boolean => entry.actions.includes(action);

/** A provider id as typed: the bare number, an IMDb id (tt…), or a provider address the server reads the id out of. Trimmed; empty is nothing to send. */
export function typedProviderId(text: string): string | undefined {
  const value = text.trim();
  return value && value.length <= 2048 && !/[\x00-\x1f\s]/.test(value) ? value : undefined;
}

/** Libraries with a needs-attention list: film and TV titles the providers could not match, music MusicBrainz could not settle, books with no author. */
export const hasAttentionList = (libraryKind: string): boolean => ['movie', 'tv', 'anime', 'music', 'audiobook'].includes(libraryKind);
