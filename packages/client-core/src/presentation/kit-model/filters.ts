import type {BrowseOperator, BrowseScalar} from '../../browse.ts';
import {part, type FactPart, type KitMessageId, type TextEnvironment} from './text.ts';
import {rangeSelectionLabel} from '../browse-toolbar.ts';

export type FilterOperator = BrowseOperator;
export type FilterValue = BrowseScalar | null | undefined;
export type AppliedFilter = Readonly<{field: string; operator: FilterOperator; value?: FilterValue | readonly FilterValue[]}>;
export type AppliedFilterModel = Readonly<{
  fieldId: string; field: FactPart; operator: FilterOperator; operatorPart: FactPart;
  rawValue: AppliedFilter['value']; values: readonly FactPart[]; negated: boolean; presence: 'present' | 'missing' | undefined;
}>;
const operators = {
  equals: 'next.kit.filter.equals', 'not-equals': 'next.kit.filter.not', in: 'next.kit.filter.in', 'not-in': 'next.kit.filter.notIn',
  'is-present': 'next.kit.filter.present', 'is-missing': 'next.kit.filter.missing', 'at-least': 'next.kit.filter.atLeast',
  'greater-than': 'next.kit.filter.greaterThan', 'at-most': 'next.kit.filter.atMost', 'less-than': 'next.kit.filter.lessThan',
  between: 'next.kit.filter.between', 'starts-with': 'next.kit.filter.startsWith',
  contains: 'next.kit.filter.contains', 'contains-any': 'next.kit.filter.containsAny', 'contains-all': 'next.kit.filter.containsAll',
} as const satisfies Record<Exclude<FilterOperator, 'within-last-days'>, KitMessageId>;

/** Value labels come from the capability owner; arbitrary metadata punctuation is preserved. */
export function appliedFilterParts(predicate: AppliedFilter, fieldName: (id: string) => string, env: TextEnvironment, valueName: (value: string | number) => string = String): AppliedFilterModel {
  const {t} = env, fieldId = predicate.field === 'decade' ? 'year' : predicate.field;
  // H10: a range on a field with a unit reads as one phrase in its preset words ("60–90 min"),
  // never as wire values with an operator ("between 3600000 5400000").
  const ranged = rangeSelectionLabel(predicate.field, [predicate as Parameters<typeof rangeSelectionLabel>[1][number]], t as Parameters<typeof rangeSelectionLabel>[2]);
  if (ranged) return Object.freeze({rawValue: predicate.value, fieldId, field: part('field', fieldName(fieldId), 'context'), operator: 'equals' as const,
    operatorPart: part('operator', '', 'context'), values: Object.freeze([part('value-0', ranged)]), negated: false, presence: undefined});
  const presence = predicate.operator === 'is-present' ? 'present' : predicate.operator === 'is-missing' ? 'missing' : undefined;
  const raw = Array.isArray(predicate.value) ? predicate.value : [predicate.value];
  const relative = predicate.operator === 'within-last-days';
  const values = presence || relative ? [] : raw.filter((v): v is string | number | boolean => v !== null && v !== undefined && (typeof v !== 'number' || Number.isFinite(v))).map((v, i) => part(`value-${i}`, typeof v === 'boolean' ? t(v ? 'next.kit.yes' : 'next.kit.no') : predicate.field === 'decade' ? t('next.kit.filter.decade', {year: String(v)}) : valueName(v), 'value'));
  return Object.freeze({rawValue: predicate.value, fieldId, field: part('field', fieldName(fieldId), 'context'), operator: predicate.operator,
    operatorPart: part('operator', predicate.operator === 'within-last-days' ? predicate.value === 365 ? t('filter.preset.lastYear') : t('filter.preset.lastDays', {count: Number(predicate.value)}) : t(operators[predicate.operator]), 'context'), values: Object.freeze(values),
    negated: predicate.operator === 'not-equals' || predicate.operator === 'not-in' || predicate.operator === 'is-missing', presence,
  });
}
