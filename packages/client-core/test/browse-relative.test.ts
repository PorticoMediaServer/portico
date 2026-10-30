import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {parseBrowseNode, validateBrowseQuery, type BrowseFieldCapability, type BrowseCapabilities} from '../src/browse.ts';
import {rangePresets, activeRangePreset, appliedFilterLabel} from '../src/presentation/browse-toolbar.ts';

const dateField = (id: string): BrowseFieldCapability => ({id, type: 'date', labelKey: `browse.field.${id}`, controlHint: 'date-range', complexity: 'standard', cost: 'indexed', operators: ['at-least', 'within-last-days'], applicableKinds: ['movie']});

test('date presets emit relative operators and stay active after time moves', () => {
  for (const id of ['dateAdded', 'releaseDate', 'lastPlayedAt']) {
    const field = dateField(id);
    const presets = rangePresets(field, new Date('2026-10-23T12:00:00Z'));
    assert.deepEqual(presets.map(p => p.predicates), [7, 30, 90, 365].map(value => [{field: id, operator: 'within-last-days', value}]));
    for (const [index, days] of [7, 30, 90, 365].entries()) {
      assert.equal(activeRangePreset(rangePresets(field, new Date('2027-01-01T00:00:00Z')), presets[index].predicates), `last-${days}`);
      assert.equal(appliedFilterLabel(presets[index].predicates[0], () => 'Date added', defaultI18n.t), days === 365 ? defaultI18n.t('filter.preset.lastYear') : `In the last ${days} days`);
    }
  }
});

test('relative values are bounded whole day counts on both parsing and query validation', () => {
  const capabilities: BrowseCapabilities = {
    library: {id: 'library', name: 'Library', kind: 'movie', defaultView: 'browse', pinned: false},
    pivots: [], resolvedPivot: null, fields: [dateField('dateAdded')], sorts: [], quickFilters: [],
    queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 8192, maximumSorts: 3, defaultLimit: 50, maximumLimit: 200, cursorTtlMs: 60000},
  };
  for (const value of [1, 7, 30, 36500]) {
    const node = parseBrowseNode({field: 'dateAdded', operator: 'within-last-days', value});
    assert.deepEqual(node, {field: 'dateAdded', operator: 'within-last-days', value});
    assert.doesNotThrow(() => validateBrowseQuery(capabilities, node));
  }
  for (const value of [undefined, null, '30', 0, -1, 36501, 1.5, Infinity, true, [], {}]) {
    const node = {field: 'dateAdded', operator: 'within-last-days', value};
    assert.throws(() => parseBrowseNode(node));
    assert.throws(() => validateBrowseQuery(capabilities, node as never));
  }
});

test('kit filter parts reuse preset words for relative recency', async () => {
  const {appliedFilterParts} = await import('../src/presentation/kit-model/filters.ts');
  for (const value of [30, 365]) {
    const parts = appliedFilterParts({field:'dateAdded',operator:'within-last-days',value}, ()=>'Date added', {t:defaultI18n.t});
    assert.equal(parts.operatorPart.text, value === 365 ? defaultI18n.t('filter.preset.lastYear') : 'In the last 30 days');
    assert.deepEqual(parts.values, []);
  }
});
