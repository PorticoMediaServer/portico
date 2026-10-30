import {browseFields, type BrowseCapabilities, type BrowseFieldCapability, type BrowseNode, type BrowseOperator, type BrowseScalar} from '../../../../../packages/client-core/src/browse';
import {activeRangePreset, browseValueLabel, fieldSelectionSummary, filterControlOf, filterValuesField, orderFilterValues, rangePresets} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {FilterFieldState, FilterFieldValue} from '../../kit/browse';
import type {NativeLibraryFacet} from './filters';

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const leaves = (nodes: readonly BrowseNode[]): BrowseNode[] => nodes.flatMap(node => 'all' in node ? leaves(node.all) : [node]);
const targets = (field: BrowseFieldCapability) => new Set([field.id, filterValuesField(field).field]);
const belongs = (node: BrowseNode, ids: ReadonlySet<string>) => {const fields = browseFields([node]); return fields.length > 0 && fields.every(id => ids.has(id));};
const scalarList = (value: BrowseScalar | readonly BrowseScalar[] | null) => value === null ? [] : Array.isArray(value) ? value : [value];

/** UI projections keep the published query whole. Editing a field replaces only
 * that field's conditions; cross-field alternatives and immutable tab predicates
 * survive without flattening an OR into an AND. */
export function libraryFilterField(field: BrowseFieldCapability, predicates: readonly BrowseNode[], facet: NativeLibraryFacet | undefined, now: Date): FilterFieldState {
  const ids = targets(field), mine = leaves(predicates).filter(node => belongs(node, ids));
  const direct = mine.filter((node): node is Extract<BrowseNode, {field: string}> => 'field' in node);
  const summary = fieldSelectionSummary(field, direct, value => browseValueLabel(String(value)));
  const presentation = {active: mine.length > 0, summary};
  const control = filterControlOf(field);
  if (control === 'toggle') return {...presentation, kind: 'toggle', value: direct.find(node => node.operator === 'equals' && typeof node.value === 'boolean')?.value as boolean | undefined ?? null};
  if (control === 'choice') return {...presentation, kind: 'choice', value: direct.find(node => node.operator === 'equals' && typeof node.value === 'string')?.value as string | undefined ?? null,
    options: (field.allowedValues ?? []).map(value => ({value, label: browseValueLabel(value)}))};
  if (control === 'range') {
    const presets = rangePresets(field, now);
    return {...presentation, kind: 'range', value: activeRangePreset(presets, direct) ?? null,
      presets: presets.map(preset => ({value: preset.id, label: defaultI18n.t(preset.label.id, preset.label.values)}))};
  }
  if (control === 'text') return {...presentation, kind: 'text', value: direct.find(node => typeof node.value === 'string')?.value as string | undefined ?? ''};
  const included: string[] = [], excluded: string[] = [];
  let mode: 'any' | 'all' | 'none' = 'any';
  for (const node of mine) {
    const negative = 'not' in node, leaf = negative ? node.not : node;
    if (!('field' in leaf)) continue;
    if (!['equals', 'in', 'contains-any', 'contains-all', 'not-in', 'not-equals'].includes(leaf.operator)) continue;
    const out = negative || leaf.operator === 'not-in' || leaf.operator === 'not-equals' ? excluded : included;
    out.push(...scalarList(leaf.value).map(String));
    if (leaf.operator === 'contains-all' && !negative) mode = 'all';
  }
  const values = orderFilterValues(field, facet?.value?.values ?? []).map(value => ({...value,
    label: field.id === 'year' ? defaultI18n.t('next.kit.filter.decade', {year: value.value}) : value.label}));
  const valueSummary = field.id === 'year' ? fieldSelectionSummary({...field, id: 'decade'}, direct, value => defaultI18n.t('next.kit.filter.decade', {year: String(value)})) : undefined;
  return {...presentation, summary: valueSummary, kind: 'values', included: [...new Set(included)], excluded: [...new Set(excluded)], mode,
    allowAll: field.operators.includes('contains-all'), cycleExclusion: true, showMatchMode: false, values,
    search: facet?.search ?? '', total: facet?.value?.total, status: facet?.phase === 'error' ? 'error' : !facet || facet.phase === 'loading' || facet.phase === 'idle' ? 'loading' : 'ready'};
}

export function libraryFilterChange(capabilities: BrowseCapabilities, field: BrowseFieldCapability, predicates: readonly BrowseNode[], value: FilterFieldValue, fixed: readonly BrowseNode[], now: Date): readonly BrowseNode[] {
  const ids = targets(field), retained = predicates.filter(node => !belongs(node, ids) || fixed.some(base => same(base, node)));
  const add: BrowseNode[] = [];
  const condition = (id: string, operator: BrowseOperator, next: BrowseScalar | readonly BrowseScalar[] | null): BrowseNode => ({field: id, operator, value: next});
  if (value.kind === 'values') {
    const target = filterValuesField(field), offered = capabilities.fields.find(candidate => candidate.id === target.field);
    if (!offered) throw new Error('native_library_filter_field_not_offered');
    const cast = (raw: readonly string[]): BrowseScalar[] => raw.map(item => offered.type === 'number' ? Number(item) : item);
    const operator = target.operator as BrowseOperator;
    if (value.included.length) {
      const node = condition(target.field, value.mode === 'all' && offered.operators.includes('contains-all') ? 'contains-all' : operator, cast(value.included));
      add.push(value.mode === 'none' ? {not: node} : node);
    }
    if (value.excluded.length) add.push({not: condition(target.field, operator, cast(value.excluded))});
  } else if (value.kind === 'range') {
    const preset = rangePresets(field, now).find(candidate => candidate.id === value.value);
    if (value.value !== null && !preset) throw new Error('native_library_filter_range_not_offered');
    add.push(...(preset?.predicates ?? []) as readonly BrowseNode[]);
  } else if (value.kind === 'toggle' || value.kind === 'choice') {
    if (value.value !== null) add.push(condition(field.id, 'equals', value.value));
  } else if (value.value) {
    const operator = field.operators.includes('contains') ? 'contains' : field.operators.includes('equals') ? 'equals' : undefined;
    if (!operator) throw new Error('native_library_filter_text_not_offered');
    add.push(condition(field.id, operator, value.value));
  }
  return [...retained, ...add];
}
