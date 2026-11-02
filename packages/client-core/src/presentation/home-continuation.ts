import type {CardTextModel} from './kit-model/facts.ts';

/** The Home rows whose cards continue a listen or a book (HM-4). */
export const homeContinuationRow = (rowId: string): boolean => rowId === 'listen_next' || rowId === 'continue_books';

/**
 * A continuing listen or book draws its continuation where the author or artist line otherwise
 * sits, from the row's own resume target through the shared card-text `continuation` parts
 * (DECISIONS.md, Web Home Listen next continuation): Continue Listening the time left
 * ("4 h 32 min left", phone-app f2), Continue your books the chapter and the time left
 * ("Chapter 5 · 9 h 12 min left", tablet-read f11). The card keeps its own title and identity,
 * and draws the target's progress. Without a known time left nothing is guessed: the card stays
 * as it was (api/home.md, "No duration or remaining time is guessed").
 */
export function homeContinuationCard(rowId: string, card: CardTextModel, continued: CardTextModel): CardTextModel {
  if (!homeContinuationRow(rowId)) return card;
  const left = continued.detail.find(part => part.id === 'remaining');
  if (!left) return card;
  const caption = rowId === 'continue_books' ? continued.detail.filter(part => part.id === 'chapter' || part.id === 'remaining') : [left];
  return Object.freeze({...card, caption: Object.freeze(caption), detail: Object.freeze([]), ...(continued.progress === undefined ? {} : {progress: continued.progress})});
}
