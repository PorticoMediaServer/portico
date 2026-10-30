import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {selectionUnavailableActions} from '../src/screens/browse/selection-unavailable-actions';
import {wholeViewQuery} from '../src/screens/browse/selection-query';
import {createQuerySelectionEditor} from '../src/screens/browse/selection-query-editor';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

const unavailableActions = {play: 'unsupported-sort', queue: 'unsupported-sort', playlist: 'unsupported-sort', metadataPreview: 'preview-limit'} as const;
const count = 201, basis = 'b'.repeat(64), revision = `browse_${'a'.repeat(64)}`;
const every = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: true, edit: true, refresh: true, delete: true};
const descriptor = {libraryId: 'lib', pivot: 'movies', filter: null, sort: [{field: 'title', direction: 'asc'}], catalogRevision: revision, selectionBasis: basis, total: count, unavailableActions,
  types: [{kind: 'movie', count, libraries: [{libraryId: 'lib', count}], capabilities: Object.fromEntries(Object.keys(every).map(key => [key, count]))}]};
test('web query mapping retains optional exclusions, proof and truthful counts', () => {
  const query = wholeViewQuery(descriptor as never);
  assert.deepEqual(query.summary.unavailableActions, unavailableActions);
  assert.equal(query.summary.types[0].capabilityCounts.play, 201);assert.equal(query.summary.every.edit, true);
  assert.equal('query' in query.selector && query.selector.query.selectionBasis, basis);
});
test('web exclusions replace only already offered callbacks with concise reasons', () => {
  const callbacks = {onPlay() {}, onQueue() {}, onPlaylist() {}, onEdit() {}, onMyList() {}};
  const actions = selectionUnavailableActions(callbacks, unavailableActions);
  assert.equal(actions.onPlay, undefined);assert.equal(actions.onQueue, undefined);assert.equal(actions.onEdit, undefined);assert.equal(actions.onPlaylist, undefined);
  assert.equal(actions.playlistDisabledReason, defaultI18n.t('next.selection.unsupportedSort'));
  assert.equal(actions.playDisabledReason, defaultI18n.t('next.selection.unsupportedSort'));
  assert.equal(actions.queueDisabledReason, actions.playDisabledReason);
  assert.equal(actions.editDisabledReason, defaultI18n.t('next.selection.previewLimit'));
  assert.equal(actions.onMyList, callbacks.onMyList);assert.equal(callbacks.onPlay instanceof Function, true);
  assert.deepEqual(selectionUnavailableActions({}, unavailableActions), {});
  assert.deepEqual(selectionUnavailableActions(callbacks, undefined), callbacks);
});
test('actual desktop and phone controls show unavailable Play/Playlist/Edit and explain Queue without invoking stale callbacks', async () => {
  const host = createRenderer();Object.assign(host.react, {useId: () => 'exclusion'});
  const {SelectionBar, PhoneActionBar} = await componentModule(new URL('../src/kit/selection/SelectionBar.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n}, '../controls': {Button: 'Button'}, '../Icon': {Icon: 'Icon'},
    '../overlays': {Menu: 'Menu', MenuGroup: 'MenuGroup', MenuItem: 'MenuItem'}, '../overlays/shared': {usePhone: () => false},
    './copy': {selectionText: (key: string, values: any) => defaultI18n.t(key as never, values)}, './presentation': {selectionCountText: () => '201 movies'}, './selection.module.css': {default: {}},
  });
  let calls = 0;const reason = defaultI18n.t('next.selection.unsupportedSort'), preview = defaultI18n.t('next.selection.previewLimit');
  const actions = {onPlay() {calls++;}, onQueue() {calls++;}, onPlaylist() {calls++;}, onEdit() {calls++;}, onDelete() {calls++;}, deleteDisabledReason: reason, playDisabledReason: reason, queueDisabledReason: reason, playlistDisabledReason: reason, editDisabledReason: preview};
  try {
    for (const component of [() => SelectionBar({selection: {count}, actions, onExit() {}}), () => PhoneActionBar({selection: {count}, actions})]) {
      const nodes = tree(host.render(component));
      const play = nodes.find((node: any) => node.type === 'Button' && node.props.title === reason);
      assert(play);assert.equal(play.props.disabled, true);assert.equal(play.props.onClick, undefined);
      const sortDisabled = nodes.filter((node: any) => node.type === 'Button' && node.props.title === reason);assert.equal(sortDisabled.length, 2);
      for (const control of sortDisabled) {assert.equal(control.props.disabled, true);assert.equal(control.props.onClick, undefined);}
      const deletion = nodes.flatMap((node: any) => node.type === 'Menu' ? node.props.items ?? [] : []).find((item: any) => item.id === 'delete')
        ?? nodes.find((node: any) => node.type === 'MenuItem' && node.props.destructive)?.props;
      assert(deletion);assert.equal(deletion.disabled, true);assert.equal(deletion.description, reason);deletion.onSelect();
      const queue = nodes.find((node: any) => node.type === 'Menu' && node.props.items?.[0]?.id === 'next');assert(queue);
      for (const item of queue.props.items) {assert.equal(item.disabled, true);assert.equal(item.description, reason);item.onSelect();}
      const edit = nodes.find((node: any) => node.type === 'Button' && node.props.title === preview);
      const editRow = nodes.flatMap((node: any) => node.type === 'Menu' ? node.props.items ?? [] : []).find((item: any) => item.id === 'edit');
      if (edit) {assert.equal(edit.props.disabled, true);assert.equal(edit.props.onClick, undefined);}
      else {assert(editRow);assert.equal(editRow.disabled, true);assert.equal(editRow.description, preview);editRow.onSelect();}
    }
    assert.equal(calls, 0);
  } finally {host.dispose();}
});
test('metadata preview exclusion refuses before requests; small previews retain the existing proof-bearing query API', async () => {
  const descriptorQuery = wholeViewQuery(descriptor as never);
  const target = {mode: 'query', selector: descriptorQuery.selector, total: count, sourceTotal: count, summary: {count, types: [{kind: 'movie', count}], every, library: {kind: 'single', libraryId: 'lib'}, unavailableActions}, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: revision}};
  const requests: any[] = [];const stop = new Error('captured-preview-query');
  const producer = createQuerySelectionEditor({api: {request: async (...args: any[]) => {requests.push(args);throw stop;}}, serverId: 's', viewerFence: () => 'f'.repeat(64), requestId: async () => 'request', isCurrent: () => true} as never);
  await assert.rejects(producer.preview({target} as never), /query-editor-unavailable/);assert.equal(requests.length, 0);
  const small = {...target, total: 200, sourceTotal: 200, summary: {...target.summary, count: 200, types: [{kind: 'movie', count: 200}], unavailableActions: undefined}};
  await assert.rejects(producer.preview({target: small} as never), error => error === stop);
  assert.equal(requests.length, 1);assert.deepEqual(requests[0][2], {selector: descriptorQuery.selector, expected: {catalogRevision: revision}});
});
test('direct and retained Playlist picker callbacks refuse the excluded query before requests', async () => {
  const {LibraryPickerProducer} = await import('../src/screens/browse/selection-picker');
  const query = wholeViewQuery(descriptor as never);
  const target = {mode: 'query', selector: query.selector, total: count, sourceTotal: count, summary: {count, types: [{kind: 'movie', count}], every, library: {kind: 'single', libraryId: 'lib'}, unavailableActions: undefined as typeof unavailableActions | undefined}, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: revision}};
  let calls = 0;
  const producer = new LibraryPickerProducer(target as never, 'playlist', {api: {request: async () => {calls++;}}, serverId: 's', viewerId: 'v', requestId: async () => {calls++;return 'request';}, isCurrent: () => true, pickerIdentity: {authority: 'local', accountId: 'a', profileId: 'p'}} as never, true, () => {});
  const retainedChoose = () => producer.choose('playlist');
  target.summary.unavailableActions = unavailableActions;
  try {
    assert.throws(() => producer.open(), /query-destination-unavailable/);
    assert.throws(retainedChoose, /query-destination-unavailable/);
    assert.throws(() => producer.create('New playlist'), /query-destination-unavailable/);
    target.summary.unavailableActions = undefined;
    const pendingOpen = producer.open();
    target.summary.unavailableActions = unavailableActions;
    await assert.rejects(pendingOpen, /query-destination-unavailable/);
    assert.equal(calls, 0);
  } finally {producer.close();}
});
test('Delete exclusion disables only an offered bounded action and refuses retained conversion before Browse', async () => {
  const {resolveQueryDelete} = await import('../src/screens/browse/selection-delete-query');
  const {onEdit, onMyList, onCollection} = {onEdit() {}, onMyList() {}, onCollection() {}};
  const reason = defaultI18n.t('next.selection.unsupportedSort'), limit = defaultI18n.t('next.selection.tooMany');
  const actions = selectionUnavailableActions({onDelete() {}, onEdit, onMyList, onCollection}, {delete: 'unsupported-sort'});
  assert.equal(actions.onDelete, undefined);assert.equal(actions.deleteDisabledReason, reason);
  assert.equal(actions.onEdit, onEdit);assert.equal(actions.onMyList, onMyList);assert.equal(actions.onCollection, onCollection);
  assert.deepEqual(selectionUnavailableActions({}, {delete: 'unsupported-sort'}), {});
  assert.equal(selectionUnavailableActions({onDelete() {}, deleteExplanation: limit}, {delete: 'unsupported-sort'}).deleteExplanation, limit);
  assert.equal(selectionUnavailableActions({deleteDisabledReason: limit}, {delete: 'unsupported-sort'}).deleteDisabledReason, limit);
  const query = wholeViewQuery({...descriptor, total: 200, unavailableActions: {delete: 'unsupported-sort'}} as never);
  const target = {mode: 'query', selector: query.selector, total: 200, sourceTotal: 200, summary: {count: 200, types: [{kind: 'movie', count: 200}], every, library: {kind: 'single', libraryId: 'lib'}, unavailableActions: {delete: 'unsupported-sort'}}, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: revision}};
  let calls = 0;const captured: any[] = [], stop = new Error('legacy-proof-conversion');
  const ports = {api: {request: async (...args: any[]) => {calls++;captured.push(args);throw stop;}}, isCurrent: () => true};
  const retained = () => resolveQueryDelete(target as never, ports as never, new AbortController().signal);
  await assert.rejects(retained, /query-delete-unavailable/);assert.equal(calls, 0);
  const legacy = {...target, summary: {...target.summary, unavailableActions: undefined}};
  await assert.rejects(resolveQueryDelete(legacy as never, ports as never, new AbortController().signal), error => error === stop);
  assert.equal(calls, 1);assert.equal(captured[0][2].selectionBasis, basis);
});
