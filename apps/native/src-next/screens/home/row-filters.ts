import type {BrowseFieldCapability, BrowseNode, BrowseOperator} from '../../../../../packages/client-core/src/browse';
import {filterValuesField, rangePresets} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import type {FilterFieldValue, FilterValueSelection} from '../../kit/browse/types';

/** Only predicates wholly owned by this control can be replaced. A compound
 * across other fields remains intact, with its independent removal affordance. */
export function nativeRowFilterField(node: BrowseNode): string | undefined {
  if ('field' in node) return node.field;
  if ('not' in node) return nativeRowFilterField(node.not);
  const children = 'all' in node ? node.all : node.any;
  const field = children.length ? nativeRowFilterField(children[0]) : undefined;
  return field && children.every(child => nativeRowFilterField(child) === field) ? field : undefined;
}

/** Decode a negative-only facet as the drawn None mode, so editing a second
 * value does not accidentally turn the first selection into a positive match. */
export function nativeRowFilterValues(field: BrowseFieldCapability, filters: readonly BrowseNode[]): FilterValueSelection {
  const target = filterValuesField(field).field;
  const own = filters.filter(node => nativeRowFilterField(node) === target);
  const leaves = (node: BrowseNode): readonly Extract<BrowseNode, {field: string}>[] => 'field' in node ? [node] : 'not' in node ? [] : ('all' in node ? node.all : node.any).flatMap(leaves);
  const strings = (nodes: readonly Extract<BrowseNode, {field: string}>[]) => nodes.flatMap(node => Array.isArray(node.value) ? node.value.filter((value): value is string => typeof value === 'string') : []);
  const direct = own.flatMap(leaves), negative = own.flatMap(node => 'not' in node ? leaves(node.not) : []);
  const included = strings(direct), excluded = strings(negative);
  return !included.length && excluded.length ? {kind: 'values', included: excluded, excluded: [], mode: 'none'}
    : {kind: 'values', included, excluded, mode: direct.some(node => node.operator === 'contains-all') ? 'all' : 'any'};
}

export function nativeRowFilterChange(field: BrowseFieldCapability, value: FilterFieldValue, filters: readonly BrowseNode[], now: Date): readonly BrowseNode[] {
  const target = value.kind === 'values' ? filterValuesField(field) : {field: field.id};
  const rest = filters.filter(node => nativeRowFilterField(node) !== target.field);
  const next: BrowseNode[] = [];
  const condition = (operator: BrowseOperator, input: string | boolean | readonly string[]): BrowseNode => ({field: target.field, operator, value: input});
  if (value.kind === 'toggle' || value.kind === 'choice') {
    if (value.value !== null) next.push(condition('equals', value.value));
  } else if (value.kind === 'text') {
    if (value.value) next.push(condition(field.operators.includes('starts-with') ? 'starts-with' : field.operators.includes('contains') ? 'contains' : 'equals', value.value));
  } else if (value.kind === 'range') {
    if (value.value !== null) {
      const preset = rangePresets(field, now).find(candidate => candidate.id === value.value);
      if (!preset) throw new Error('native_filter_preset_not_offered');
      next.push(...preset.predicates.map(predicate => ({field: predicate.field, operator: predicate.operator as BrowseOperator, value: predicate.value as string | number | boolean | null | readonly (string | number | boolean)[]})));
    }
  } else {
    const mode = filterValuesField(field), operator = mode.operator as BrowseOperator;
    if (value.mode === 'all' && !field.operators.includes('contains-all')) throw new Error('native_filter_mode_not_offered');
    if (value.included.length) next.push(value.mode === 'none' ? {not: condition(operator, value.included)} : condition(value.mode === 'all' ? 'contains-all' : operator, value.included));
    if (value.excluded.length) next.push({not: condition(operator, value.excluded)});
  }
  return Object.freeze([...rest, ...next]);
}
