import {integer, part, supplied, type FactPart, type TextEnvironment} from './text.ts';

/** Authoritative whole-collection facts, never facts inferred from its bounded artwork preview. */
export type CollectionCaptionFacts = Readonly<{
  mediaKind?: string; itemCount?: number; firstYear?: number; lastTitle?: string;
}>;

/** The drawn Home sentence supports movie collections; unknown kinds have no invented noun. */
export function collectionCaptionParts(facts: CollectionCaptionFacts, env: TextEnvironment): readonly FactPart[] {
  if (facts.mediaKind !== 'movie' || !integer(facts.itemCount)) return Object.freeze([]);
  const text = integer(facts.firstYear) && supplied(facts.lastTitle)
    ? env.t('next.kit.collection.filmsFromTo', {count: facts.itemCount, year: facts.firstYear, title: facts.lastTitle})
    : env.t('next.kit.collection.filmsOnServer', {count: facts.itemCount});
  return Object.freeze([part('collection-summary', text, 'context')]);
}
