import {BrowseQueryBuilder, type BrowseCapabilities, type BrowseFieldCapability, type BrowseNode} from '../browse.ts';
import type {SavedDefinition} from '../personal-saved.ts';
import type {I18n} from '../../../i18n/src/index.ts';
import {activeRangePreset, appliedFilterLabel, browseNameId, browseValueLabel, orderToolbarPredicates, rangePresets, sortDirectionKind} from './browse-toolbar.ts';

/** `list` is optional so an older narrowed caller stays source-compatible; the
 * catalogue's guarded formatter is used whenever it is present. */
export type SavedViewSummaryText = Pick<I18n, 't' | 'has' | 'locale'> & Partial<Pick<I18n, 'list'>>;
export type SavedViewSummary = Readonly<{line: string; conditions: string; suggestedName: string}>;
type Condition = Extract<BrowseNode, {field: string}>;
const same = (a: Condition, b: Condition) => a.field === b.field && a.operator === b.operator && JSON.stringify(a.value) === JSON.stringify(b.value);
const quickNames: Readonly<Record<string, string>> = {
  'in-progress': 'filter.quick.inProgress', unwatched: 'filter.quick.unwatched', watched: 'filter.quick.played',
  favorites: 'filter.quick.favorites', watchlist: 'filter.quick.watchlist', available: 'filter.quick.available', missing: 'filter.quick.unavailable',
};

/** §38: rendering only. No resource summary write, query rewrite or membership read.
 * Unsupported grammar/unknown vocabulary withholds the entire line and name. */
export function savedViewSummary(definition: SavedDefinition, capabilities: BrowseCapabilities, text: SavedViewSummaryText): SavedViewSummary | undefined {
  try {
    if (definition.libraryId !== capabilities.library.id || capabilities.resolvedPivot && capabilities.resolvedPivot.id !== definition.pivot) return;
    const query = definition.query;
    const nodes = !query ? [] : 'field' in query ? [query] : 'all' in query && query.all.length > 0 && query.all.every(node => 'field' in node) ? query.all : undefined;
    if (!nodes) return;
    const conditions: Condition[] = [];
    for (const node of nodes) {if (!('field' in node)) return;conditions.push(node);}
    const builder = new BrowseQueryBuilder(capabilities, definition.pivot);
    for (const node of conditions) builder.add(node);
    for (const sort of definition.sort) builder.sort(sort.field, sort.direction, sort.seed);
    const actual = builder.build();
    if (actual.sort?.length !== 1) return; // The toolbar has one sort; never omit a second order.
    const sort = actual.sort[0];
    const sortCap = capabilities.sorts.find(value => value.id === sort.field);
    if (!sortCap || sortCap.requiresGroup) return;
    const fieldName = (id: string): string | undefined => {
      const capability = capabilities.fields.find(field => field.id === id) ?? capabilities.sorts.find(field => field.id === id);
      const key = browseNameId(id === 'decade' ? 'year' : id, capability?.labelKey, text.has);
      return key && text.has(key) ? text.t(key) : undefined;
    };
    // Hermes builds without Intl.ListFormat must not lose the whole saved-view
    // summary; the catalogue's own guarded list formatter supplies the fallback.
    const list = (items: readonly string[]) => text.list ? text.list(items, 'or') : items.join(', ');
    const words: string[] = [], consumed = new Set<Condition>();
    const ordered = orderToolbarPredicates(conditions);
    for (const node of ordered) {
      if (consumed.has(node)) continue;
      const field = capabilities.fields.find(value => value.id === node.field);
      if (!field || !field.operators.includes(node.operator)) return;
      const label = fieldName(field.id);
      if (!label) return;
      const quick = capabilities.quickFilters.find(value => 'field' in value.query && same(node, value.query));
      if (quick) {
        const key = text.has(quick.labelKey) ? quick.labelKey : quickNames[quick.id];
        if (!key || !text.has(key)) return;
        words.push(text.t(key));continue;
      }
      const own = conditions.filter(value => value.field === node.field);
      if (field.id === 'durationMs') {
        const phrase = lengthWords(own, text);
        if (phrase) {words.push(phrase);own.forEach(value => consumed.add(value));continue;}
      }
      if (field.type === 'date' && node.operator === 'within-last-days' && typeof node.value === 'number') {
        words.push(field.id === 'dateAdded' ? text.t('savedView.summary.addedLastDays', {count: node.value}) : text.t('savedView.summary.fieldLastDays', {field: label, count: node.value}));continue;
      }
      if (field.id === 'dateAdded') {
        const absolutePresets = rangePresets(field, new Date()).map(preset => ({...preset, predicates: preset.predicates.map(p => ({...p, operator: 'at-least' as const, value: new Date(Date.now() - Number(p.value) * 86400000).toISOString().slice(0, 10)}))}));
        const preset = activeRangePreset(absolutePresets, own);
        const count = preset && /^last-(7|30|90|365)$/.exec(preset)?.[1];
        if (count) {words.push(text.t('savedView.summary.addedLastDays', {count: Number(count)}));own.forEach(value => consumed.add(value));continue;}
      }
      const decade = decadeValues(node);
      if (decade) {words.push(text.t('savedView.summary.decade', {values: list(decade.map(value => text.t('savedView.summary.decadeValue', {decade: String(value)})))}));continue;}
      if (node.operator === 'in' || node.operator === 'contains-any') {
        if (!Array.isArray(node.value) || !node.value.length) return;
        words.push(text.t('savedView.summary.fieldValues', {field: label, values: list(node.value.map(value => valueWords(field, value, text)))}));continue;
      }
      if (field.type === 'number' && node.operator === 'at-least' && typeof node.value === 'number') {
        words.push(field.id === 'communityRating' ? text.t('savedView.summary.rating', {value: node.value}) : text.t('savedView.summary.atLeast', {field: label, value: field.id === 'year' ? String(node.value) : node.value}));continue;
      }
      const phrase = appliedFilterLabel(node, id => fieldName(id) ?? '', text.t);
      if (!phrase) return;
      words.push(phrase);
    }
    const kind = sortDirectionKind(sort.field), label = fieldName(sort.field);
    let sentence: string;
    if (sort.field === 'lastPlayed' && sort.direction === 'desc') sentence = text.t('savedView.summary.lastPlayed');
    else if (kind === 'date' || kind === 'rating' || kind === 'duration') sentence = text.t('lib.direction', {kind, dir: sort.direction});
    else {
      if (!label) return;
      sentence = kind === 'alpha' ? text.t(sort.direction === 'asc' ? 'savedView.summary.alphaAscending' : 'savedView.summary.alphaDescending', {field: label})
        : text.t('savedView.summary.otherSort', {field: label, direction: text.t('lib.direction', {kind, dir: sort.direction})});
    }
    const joined = words.join(', ');
    const line = joined ? text.t('savedView.summary.line', {conditions: joined, sort: sentence}) : text.t('savedView.summary.sortOnly', {sort: sentence});
    return Object.freeze({line, conditions: joined, suggestedName: joined && [...joined].length <= 200 && !/[\x00\r\n]/.test(joined) ? joined : ''});
  } catch { return; }
}

function valueWords(field: BrowseFieldCapability, value: unknown, text: SavedViewSummaryText): string {
  if (typeof value === 'string') {
    // Metadata facet strings are labels as stored, including spelling/hyphens.
    if (field.type === 'identity-set') return value;
    const key = `filter.value.${value}`;
    return text.has(key) ? text.t(key) : browseValueLabel(value);
  }
  return typeof value === 'boolean' ? text.t(value ? 'lib.yes' : 'lib.no') : String(value);
}
function decadeValues(node: Condition): readonly number[] | undefined {
  const values = Array.isArray(node.value) ? node.value : [node.value];
  if (node.field === 'year' && node.operator === 'between' && values.length === 2 && typeof values[0] === 'number' && values[0] % 10 === 0 && values[1] === values[0] + 9) return [values[0]];
  if (node.field !== 'decade' || !['equals', 'in'].includes(node.operator) || !values.length || !values.every(value => typeof value === 'number' && Number.isInteger(value) && value % 10 === 0)) return;
  return values.filter((value): value is number => typeof value === 'number');
}
function lengthWords(nodes: readonly Condition[], text: SavedViewSummaryText): string | undefined {
  const one = nodes[0];
  if (nodes.length === 1 && one) {
    if (typeof one.value === 'number' && Number.isFinite(one.value) && one.value >= 0) {
      if (one.operator === 'at-most' || one.operator === 'less-than') return text.t('savedView.summary.lengthUnder', {minutes: one.value / 60000});
      if (one.operator === 'at-least' || one.operator === 'greater-than') return text.t('savedView.summary.lengthOver', {minutes: one.value / 60000});
    }
    if (one.operator === 'between' && Array.isArray(one.value) && one.value.length === 2 && one.value.every(value => typeof value === 'number' && Number.isFinite(value) && value >= 0)) return text.t('savedView.summary.lengthBetween', {from: Number(one.value[0]) / 60000, to: Number(one.value[1]) / 60000});
  }
  const low = nodes.find(value => value.operator === 'at-least'), high = nodes.find(value => value.operator === 'at-most');
  if (nodes.length === 2 && low && high && typeof low.value === 'number' && typeof high.value === 'number' && low.value >= 0 && low.value <= high.value) return text.t('savedView.summary.lengthBetween', {from: low.value / 60000, to: high.value / 60000});
}
