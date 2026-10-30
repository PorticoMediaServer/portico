import assert from 'node:assert/strict';
import test from 'node:test';
import {createCellNavigator, cellFocusMemory, type CellMember} from '../src-next/kit/focus/cell-navigation.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';

const grid = Array.from({length: 11}, (_, index) => ({id: String(index), cell: {index}}));
test('every grid first column has the declared Left boundary and true row ends do not wrap', () => {
  const navigate = createCellNavigator(grid, {grid: {columns: 4, count: 11}, edges: {left: 'keyboard', up: 'toolbar'}}, {column: 0});
  for (const id of ['0', '4', '8']) assert.deepEqual(navigate(id).left, {regionId: 'keyboard'});
  assert.deepEqual(navigate('5').left, {childId: '4'});
  assert.deepEqual(navigate('3').right, {childId: '3'});
  assert.deepEqual(navigate('10').right, {childId: '10'});
  assert.deepEqual(navigate('2').up, {regionId: 'toolbar'});
});
test('a short last row clamps the actual target while preserving the column for the opposite move', () => {
  const config = {grid: {columns: 4, count: 11}};
  let memory = cellFocusMemory({index: 7}, undefined, config, {});
  assert.equal(memory.column, 3);
  assert.deepEqual(createCellNavigator(grid, config, memory)('7').down, {childId: '10'});
  memory = cellFocusMemory({index: 10}, {index: 7}, config, memory);
  assert.equal(memory.column, 3);
  assert.deepEqual(createCellNavigator(grid, config, memory)('10').up, {childId: '7'});
  memory = cellFocusMemory({index: 10}, undefined, config, memory);
  assert.equal(memory.column, 3, 'lifecycle reentry preserves the short-row column');
  memory = cellFocusMemory({index: 9}, {index: 10}, config, memory);
  assert.equal(memory.column, 1, 'a genuine lateral move chooses a new column');
});
test('an unloaded adjacent cell cannot skip to a distant retained native host', () => {
  const navigate = createCellNavigator([grid[4]!, grid[10]!], {grid: {columns: 4, count: 11}}, {column: 0});
  assert.deepEqual(navigate('4').right, {childId: '4'});
  assert.deepEqual(navigate('4').up, {childId: '4'});
});
const programme = (id: string, row: number, start: number, end: number): CellMember => ({id, cell: {row, start, end, kind: 'programme'}});
const channel = (row: number): CellMember => ({id: `channel-${row}`, cell: {row, kind: 'channel'}});
const guide = [channel(0), programme('a', 0, 3.5, 4.5), programme('b', 0, 4.5, 5.5),
  channel(1), programme('c', 1, 3.5, 4.5), programme('d', 1, 4.5, 5.5),
  channel(2), programme('wide', 2, 3.5, 6.5)];
const timeConfig = {timeGrid: {initialTime: 4, start: 3, end: 7}, edges: {left: 'rail', up: 'chips'}};
test('guide vertical moves keep the chosen time through wide cells and minute updates', () => {
  let memory = cellFocusMemory(guide[1]!.cell, undefined, timeConfig, {});
  assert.equal(memory.time, 4);
  memory = cellFocusMemory(guide[2]!.cell, guide[1]!.cell, timeConfig, memory);
  assert.equal(memory.time, 4.5);
  assert.deepEqual(createCellNavigator(guide, timeConfig, memory)('b').down, {childId: 'd'});
  memory = cellFocusMemory(guide[5]!.cell, guide[2]!.cell, timeConfig, memory);
  assert.deepEqual(createCellNavigator(guide, timeConfig, memory)('d').down, {childId: 'wide'});
  memory = cellFocusMemory(guide[7]!.cell, guide[5]!.cell, timeConfig, memory);
  assert.equal(memory.time, 4.5);
  assert.deepEqual(createCellNavigator(guide, {...timeConfig, timeGrid: {...timeConfig.timeGrid, initialTime: 5}}, memory)('wide').up, {childId: 'd'});
  assert.deepEqual(createCellNavigator(guide, timeConfig, memory)('d').up, {childId: 'b'});
});
test('guide channel column, first programme, header edge and true end remain native destinations', () => {
  const navigate = createCellNavigator(guide, timeConfig, {});
  assert.deepEqual(navigate('a').left, {childId: 'channel-0'});
  assert.deepEqual(navigate('channel-0').right, {childId: 'a'});
  assert.deepEqual(navigate('channel-0').left, {regionId: 'rail'});
  assert.deepEqual(navigate('channel-1').down, {childId: 'channel-2'});
  assert.deepEqual(navigate('a').up, {regionId: 'chips'});
  assert.deepEqual(navigate('wide').right, {childId: 'wide'});
});
test('actual registry focus updates native destinations without requesting another focus host', () => {
  const registry = createFocusRegistry(), id = 'guide'; let requests = 0;
  registry.configure({id, ...timeConfig});
  for (const member of guide) registry.attach({regionId: id, childId: member.id}, {requestTVFocus() {requests++;}}, {}, member.cell);
  registry.didFocus({regionId: id, childId: 'a'}); registry.didFocus({regionId: id, childId: 'b'});
  registry.didFocus({regionId: id, childId: 'd'}); registry.didFocus({regionId: id, childId: 'wide'});
  assert.equal(requests, 0, 'there is no JS per-key focus mover');
  assert.deepEqual(registry.destinations({regionId: id, childId: 'wide'}).up, {childId: 'd'});
  registry.focus({regionId: id, childId: 'a'}, {resetCellMemory: true});
  assert.equal(requests, 1);
  assert.deepEqual(registry.destinations({regionId: id, childId: 'a'}).down, {childId: 'c'});
});
test('a focused loading time cell recovers to its genuine replacement at the retained anchor', async () => {
  const registry = createFocusRegistry(), id = 'guide', requests: string[] = [];
  registry.configure({id, ...timeConfig});
  const pending = programme('loading', 0, 4.5, 5.5), before = programme('before', 0, 3.5, 4.5);
  for (const member of [before, pending]) registry.attach({regionId: id, childId: member.id}, {requestTVFocus() {requests.push(member.id);}}, {}, member.cell);
  registry.didFocus({regionId: id, childId: before.id}); registry.didFocus({regionId: id, childId: pending.id});
  registry.attach({regionId: id, childId: pending.id}, null);
  const actual = programme('server-programme', 0, 4.25, 5.75);
  registry.attach({regionId: id, childId: actual.id}, {requestTVFocus() {requests.push(actual.id);}}, {}, actual.cell);
  await Promise.resolve(); assert.deepEqual(requests, ['server-programme']);
});

test('a channel placeholder recovers to the actual row host without inventing a channel identity', async () => {
  const registry = createFocusRegistry(), id = 'guide', calls: string[] = [];
  registry.configure({id, ...timeConfig});
  registry.attach({regionId: id, childId: 'pending-channel:1'}, {requestTVFocus() {}}, {}, {row: 1, kind: 'channel'});
  registry.didFocus({regionId: id, childId: 'pending-channel:1'});
  registry.attach({regionId: id, childId: 'pending-channel:1'}, null);
  registry.attach({regionId: id, childId: 'real-server-channel'}, {requestTVFocus() {calls.push('actual');}}, {}, {row: 1, kind: 'channel'});
  await Promise.resolve(); assert.deepEqual(calls, ['actual']);
});

test('native blur and focus may arrive in separate turns without losing a lateral guide anchor', async () => {
  const registry = createFocusRegistry(), id = 'guide'; registry.configure({id, ...timeConfig});
  for (const member of guide) registry.attach({regionId: id, childId: member.id}, {requestTVFocus() {registry.didFocus({regionId: id, childId: member.id});}}, {}, member.cell);
  registry.didFocus({regionId: id, childId: 'a'}); registry.didBlur({regionId: id, childId: 'a'}); await Promise.resolve();
  registry.didFocus({regionId: id, childId: 'b'}); assert.equal(registry.cellPosition(id)?.time, 4.5);
  registry.focus({regionId: id, childId: 'a'}, {resetCellMemory: true});
  assert.equal(registry.cellPosition(id)?.time, 4, 'explicit Back starts at the now-line rather than the programme start');
});

test('channel Right enters the actual loaded row before adjacent-block loading frontiers', () => {
  const pending = {...programme('previous-block', 0, 2.5, 3.5), cell: {row: 0, kind: 'programme' as const, start: 2.5, end: 3.5, boundary: true}};
  const navigate = createCellNavigator([pending, ...guide], timeConfig, {});
  assert.deepEqual(navigate('channel-0').right, {childId: 'a'});
  assert.deepEqual(navigate('a').left, {childId: 'previous-block'});
});

test('an ordered action region uses the same native grid contract without caller indices or handles', () => {
  const registry = createFocusRegistry(); let requests = 0;
  registry.configure({id: 'actions', order: ['watch', 'record', 'series', 'favorite'], grid: {columns: 1, count: 4}});
  for (const childId of ['watch', 'record', 'series', 'favorite']) registry.attach({regionId: 'actions', childId}, {requestTVFocus() {requests++;}});
  registry.didFocus({regionId: 'actions', childId: 'record'});
  assert.deepEqual(registry.destinations({regionId: 'actions', childId: 'record'}).down, {childId: 'series'});
  assert.deepEqual(registry.destinations({regionId: 'actions', childId: 'series'}).down, {childId: 'favorite'});
  assert.equal(requests, 0);
});

test('a focused grid loading slot is filled by the real new card at that source index', async () => {
  const registry = createFocusRegistry(), calls: string[] = [];
  registry.configure({id: 'results', grid: {columns: 4, count: 5}, order: ['a', 'b', 'c', 'd', 'cursor']});
  for (const [index, childId] of ['a', 'b', 'c', 'd', 'cursor'].entries()) registry.attach({regionId: 'results', childId},
    {requestTVFocus() {calls.push(childId);}}, {}, {index, pending: childId === 'cursor'});
  registry.didFocus({regionId: 'results', childId: 'cursor'});
  registry.attach({regionId: 'results', childId: 'cursor'}, null);
  registry.attach({regionId: 'results', childId: 'served-next-card'}, {requestTVFocus() {calls.push('served-next-card');}}, {}, {index: 4});
  await Promise.resolve(); assert.deepEqual(calls, ['served-next-card']);
});


// FX096 authored for hourly CI; UNRUN at source handoff. Native key delivery is unqualified.
test('a numeric pad deliberate bottom-row hole exits while the ordinary grid clamp is unchanged', () => {
  const keys = ['1', '2', '3', '4', '5', '6', '7', '8', '9', 'delete', '0'];
  const members = keys.map((id, index) => ({id, cell: {index}}));
  const config = {grid: {columns: 3, count: keys.length, shortRow: 'exit' as const}, edges: {down: 'help'}};
  for (const [from, direction, to] of [
    ['1', 'down', '4'], ['4', 'up', '1'], ['2', 'up', '2'],
    ['8', 'down', '0'], ['7', 'down', 'delete'], ['delete', 'right', '0'], ['0', 'right', '0'],
  ] as const) {
    const memory = cellFocusMemory(members.find(member => member.id === from)!.cell, undefined, config, {});
    assert.deepEqual(createCellNavigator(members, config, memory)(from)[direction], {childId: to});
  }
  for (const from of ['9', '0', 'delete']) {
    const memory = cellFocusMemory(members.find(member => member.id === from)!.cell, undefined, config, {});
    assert.deepEqual(createCellNavigator(members, config, memory)(from).down, {regionId: 'help'});
  }
  assert.deepEqual(createCellNavigator(members, {grid: {columns: 3, count: 11}}, {column: 2})('9').down, {childId: '0'});
});
test('numeric Help Up uses the same pad original key memory without a command or guessed column', () => {
  const registry = createFocusRegistry(), requests: string[] = [];
  registry.configure({id: 'pad', entry: '1', order: ['1', '0'], grid: {columns: 3, count: 11, shortRow: 'exit'}});
  for (const childId of ['1', '0']) registry.attach({regionId: 'pad', childId}, {requestTVFocus() {requests.push(childId);}});
  registry.configure({id: 'help', entry: 'help', edges: {up: 'pad'}});
  registry.attach({regionId: 'help', childId: 'help'}, {requestTVFocus() {requests.push('help');}});
  registry.didFocus({regionId: 'pad', childId: '0'}); registry.didFocus({regionId: 'help', childId: 'help'});
  assert.equal(registry.entryAddress('pad')?.childId, '0');
  assert.deepEqual(requests, [], 'semantic native destination selection never moves focus in JavaScript');
});
