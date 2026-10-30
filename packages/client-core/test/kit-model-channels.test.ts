import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {channelMonograms, programmeTimeRange, programmeFactParts, type ChannelMarkDomain, type KitTranslate, type LineupChannel} from '../src/presentation/kit-model/index.ts';
const t: KitTranslate = (id, ...args) => formatMessage(shared[id], args[0]);
const env = {t, locale: 'en-US', timeZone: 'UTC', hourCycle: 'h12'} as const;
const ms = (s: string) => Date.parse(s);
const lineup: readonly LineupChannel[] = [
  {sourceId: 's', id: 'n', name: 'Northwind News', number: '101'}, {sourceId: 's', id: 'n24', name: 'Northwind News 24', number: '102'},
  {sourceId: 's', id: 'ss', name: 'Summit Sports', number: '201'}, {sourceId: 's', id: 'sse', name: 'Summit Sports Extra', number: '202'},
  {sourceId: 's', id: 'tm', name: 'Tidal Music TV', number: '601'}, {sourceId: 's', id: 'hc', name: 'Harbor City Local', number: '701'},
];

test('the complete authorized source domain produces NN/N24, SS/SSE, TM/HC without name exceptions', () => {
  const result = channelMonograms({kind: 'complete-authorized', channels: lineup});
  const byId = result.get('s')!;
  for (const [id, text] of Object.entries({n: 'NN', n24: 'N24', ss: 'SS', sse: 'SSE', tm: 'TM', hc: 'HC'})) assert.deepEqual(byId.get(id), {sourceId: 's', id, text, seed: `s:${id}`});
  assert.deepEqual(result, channelMonograms({kind: 'complete-authorized', channels: [...lineup].reverse()}));
  const otherNames = lineup.map(c => ({...c, name: c.name.replace('Northwind', 'Nautical').replace('Summit', 'Solar')}));
  assert.deepEqual([...channelMonograms({kind: 'complete-authorized', channels: otherNames}).get('s')!.values()].map(c => c.text), [...byId.values()].map(c => c.text));
});

test('collisions, identical IDs on different sources and source-scoped marks are deterministic', () => {
  const domain = [{sourceId: 'one', id: 'a', name: 'Alpha News'}, {sourceId: 'one', id: 'b', name: 'Alpha News'}, {sourceId: 'one', id: 'c', name: 'Alpha News'}, {sourceId: 'two', id: 'a', name: 'Alpha News'}];
  const output = channelMonograms({kind: 'complete-authorized', channels: domain});
  assert.deepEqual([...output.get('one')!.values()].map(c => c.text), ['AN', 'AN1', 'AN2']);
  assert.equal(output.get('two')!.get('a')!.text, 'AN');
  assert.notEqual(output.get('two')!.get('a')!.seed, output.get('one')!.get('a')!.seed);
  assert.deepEqual(output, channelMonograms({kind: 'complete-authorized', channels: [domain[2]!, domain[0]!, domain[3]!, domain[1]!]}));
  assert.throws(() => channelMonograms({kind: 'complete-authorized', channels: [domain[0]!, domain[0]!]}), /invalid_channel_identity/);
});

test('authoritative Home marks survive pagination and filtering exactly, with no recomputation', () => {
  const channels = [{sourceId: 's', id: 'n24', mark: {text: 'N24', seed: 's:n24'}}];
  assert.deepEqual(channelMonograms({kind: 'authoritative', channels}).get('s')!.get('n24'), {sourceId: 's', id: 'n24', text: 'N24', seed: 's:n24'});
  assert.deepEqual(channels, [{sourceId: 's', id: 'n24', mark: {text: 'N24', seed: 's:n24'}}]);
  assert.throws(() => channelMonograms({kind: 'partial', channels: lineup.slice(0, 4)} as unknown as ChannelMarkDomain), /incomplete_channel_domain/);
  assert.throws(() => channelMonograms({channels: lineup.slice(0, 4)} as unknown as ChannelMarkDomain), /incomplete_channel_domain/);
  assert.throws(() => channelMonograms({kind: 'authoritative', channels: [{sourceId: 's', id: 'c', mark: {text: '', seed: 's:c'}}]}), /missing_authoritative_channel_mark/);
});

test('reserved bases prevent an expanded collision from stealing another channel mark', () => {
  const channels = [{sourceId: 's', id: 'a', name: 'North News'}, {sourceId: 's', id: 'b', name: 'North News 24'}, {sourceId: 's', id: 'c', name: 'N24'}];
  const result = [...channelMonograms({kind: 'complete-authorized', channels}).get('s')!.values()];
  assert.equal(new Set(result.map(c => c.text)).size, 3);
  assert.deepEqual(channelMonograms({kind: 'complete-authorized', channels}), channelMonograms({kind: 'complete-authorized', channels: channels.reverse()}));
});

test('same-meridiem ranges read 3:30 to 4:30 PM, without a middle dot or repeated period', () => {
  assert.deepEqual(programmeTimeRange(ms('2026-10-21T15:30:00Z'), ms('2026-10-21T16:30:00Z'), env), {start: '3:30', end: '4:30 PM', text: '3:30 to 4:30 PM', spansDays: false});
  assert.equal(programmeTimeRange(ms('2026-10-21T03:30:00Z'), ms('2026-10-21T04:30:00Z'), env)?.text, '3:30 to 4:30 AM');
});
test('noon and midnight keep truthful meridiems and dates', () => {
  assert.equal(programmeTimeRange(ms('2026-10-21T11:30:00Z'), ms('2026-10-21T12:30:00Z'), env)?.text, '11:30 AM to 12:30 PM');
  assert.equal(programmeTimeRange(ms('2026-10-21T12:00:00Z'), ms('2026-10-21T13:00:00Z'), env)?.text, '12:00 to 1:00 PM');
  assert.equal(programmeTimeRange(ms('2026-10-21T00:00:00Z'), ms('2026-10-21T01:00:00Z'), env)?.text, '12:00 to 1:00 AM');
  assert.equal(programmeTimeRange(ms('2026-10-21T23:30:00Z'), ms('2026-10-22T00:30:00Z'), env)?.text, 'Oct 21, 2026, 11:30 PM to Oct 22, 2026, 12:30 AM');
  assert.equal(programmeTimeRange(ms('2026-10-21T15:00:00Z'), ms('2026-10-22T16:00:00Z'), env)?.text, 'Oct 21, 2026, 3:00 PM to Oct 22, 2026, 4:00 PM');
});
test('clock follows device zone and 24-hour choice, including midnight and DST repeated hour', () => {
  const a = ms('2026-10-21T18:30:00Z'), b = ms('2026-10-21T19:30:00Z');
  assert.equal(programmeTimeRange(a, b, {...env, timeZone: 'America/Halifax'})?.text, '3:30 to 4:30 PM');
  assert.equal(programmeTimeRange(a, b, {...env, hourCycle: 'h23'})?.text, '18:30 to 19:30');
  assert.equal(programmeTimeRange(ms('2026-10-21T00:00:00Z'), ms('2026-10-21T01:00:00Z'), {...env, hourCycle: 'h23'})?.text, '00:00 to 01:00');
  assert.equal(programmeTimeRange(a, b, {...env, locale: 'fr-FR', hourCycle: 'h23'})?.text, '18:30 to 19:30');
  assert.match(programmeTimeRange(ms('2026-11-01T05:30:00Z'), ms('2026-11-01T06:30:00Z'), {...env, timeZone: 'America/New_York'})!.text, /^1:30 AM GMT-4 to 1:30 AM GMT-5$/);
});
test('invalid/reversed time, invalid zone, and out-of-range instants produce no range', () => {
  for (const [a, b] of [[NaN, 10], [0, Infinity], [-Infinity, 0], [10, 10], [10, 0], [1e20, 1e20 + 1e8]]) assert.equal(programmeTimeRange(a!, b!, env), undefined);
  assert.equal(programmeTimeRange(0, 3600_000, {...env, timeZone: 'Mars/Olympus'}), undefined);
});
test('programme flags use true supplied flags, ordered independently of airtime and remaining', () => {
  const startMs = ms('2026-10-21T15:30:00Z'), endMs = ms('2026-10-21T16:30:00Z'), now = ms('2026-10-21T16:00:00Z');
  const base = {startMs, endMs, episode: {mode: 'seasonal', season: 5, episode: 9}, subtitle: 'Crossed Wires', categories: ['Drama'], rating: 'TV-PG'} as const;
  assert.deepEqual(programmeFactParts(base, env, now).map(p => [p.id, p.text, p.role]), [['time-range', '3:30 to 4:30 PM', 'context'], ['episode-label', 'S5 E9', 'context'], ['programme-subtitle', 'Crossed Wires', 'identity'], ['category-0', 'Drama', 'value'], ['content-rating', 'TV-PG', 'supplement'], ['started', 'Started 30 min ago', 'supplement'], ['remaining', '30 min left', 'supplement']]);
  assert.deepEqual(programmeFactParts({...base, flags: {live: true, new: true, premiere: true}, recording: true}, env).slice(-4).map(p => p.text), ['Live', 'New', 'Premiere', 'Recording']);
  assert.equal(programmeFactParts({...base, flags: {live: false}}, env, now).some(p => p.id === 'live'), false);
  assert.deepEqual(programmeFactParts({}, env), []);
  assert.equal(programmeFactParts(base, env, endMs).some(p => p.id === 'remaining'), false);
});
