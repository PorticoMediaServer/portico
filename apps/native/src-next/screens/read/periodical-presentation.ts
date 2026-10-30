import type {ReadingPeriodicalIssue} from '../../../../../packages/client-core/src';
import type {NativeReadPeriodicalState} from './read-periodical-model';

/** Newspapers follow the selected date; a pending/missing date must never
 * silently offer the latest edition's Read action or artwork. */
export function periodicalHeroIssue(state: NativeReadPeriodicalState, magazine: boolean): ReadingPeriodicalIssue | undefined {
  if (magazine) return state.latest;
  if (!state.selectedDate) return undefined;
  return (state.selectedDateItems ?? state.items).find(issue => issue.issueDate === state.selectedDate);
}

/** The date's actual day-query cover may be beyond the month thumbnail page. */
export function periodicalDatePreview(state: NativeReadPeriodicalState, date: string): ReadingPeriodicalIssue | undefined {
  return state.selectedDateItems?.find(issue => issue.issueDate === date) ?? state.items.find(issue => issue.issueDate === date);
}

/** Display-only optional mark. Its identity and revision must belong to this
 * actual item; absence or malformed facts never imply that a book was read. */
export function readingItemIsRead(item: unknown): boolean {
  if (!item || typeof item !== 'object' || !('id' in item) || !('readingMark' in item)) return false;
  const mark = item.readingMark;
  return !!mark && typeof mark === 'object' && 'itemId' in mark && mark.itemId === item.id && 'read' in mark && mark.read === true
    && 'revision' in mark && typeof mark.revision === 'number' && Number.isSafeInteger(mark.revision) && mark.revision >= 0;
}

export function periodicalDayRead(state: NativeReadPeriodicalState, date: string): boolean {
  const count = state.days.find(day => day.value === date)?.count;
  const items = state.selectedDate === date && state.selectedDateItems ? state.selectedDateItems : state.items.filter(issue => issue.issueDate === date);
  return count !== undefined && count > 0 && items.length === count && items.every(issue => readingItemIsRead(issue.item));
}
