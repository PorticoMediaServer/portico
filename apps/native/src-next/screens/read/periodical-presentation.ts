import type {ReadingPeriodicalIssue} from '../../../../../packages/client-core/src';
import {defaultI18n} from '../../../../../packages/i18n/src';
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

/** An issue's month and year ("December 1929"), or its full date for a dated issue; undefined when the date is not one. */
export function issueMonth(date: string | undefined, withYear = true): string | undefined {
  const match = date ? /^(\d{4})-(\d{2})/.exec(date) : null;
  if (!match) return undefined;
  return new Intl.DateTimeFormat(defaultI18n.locale, {month: 'long', ...(withYear ? {year: 'numeric'} : {}), timeZone: 'UTC'})
    .format(Date.UTC(Number(match[1]), Number(match[2]) - 1, 1));
}

/** An issue card's title under its year's heading (phone-read 24): its month ("December"), or its month and day for a
 * dated issue ("August 27"); undefined when the date is not one. */
export function issueCardTitle(date: string): string | undefined {
  const match = /^(\d{4})-(\d{2})(?:-(\d{2}))?$/.exec(date);
  if (!match) return undefined;
  return new Intl.DateTimeFormat(defaultI18n.locale, {month: 'long', ...(match[3] ? {day: 'numeric'} : {}), timeZone: 'UTC'})
    .format(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3] ?? 1)));
}
