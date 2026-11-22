import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../../packages/i18n/src';
import * as registryModule from '../../src-next/kit/focus/registry';
import * as selectionUnavailableActions from '../../src-next/screens/library/selection-unavailable-actions.ts';
import {createFabricHookHost} from './fabric-hooks.mjs';
import {screenModule} from './screen-harness.mjs';
import type {LibraryFooterBindings} from '../next-tv-actionable-focus.test.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

export function registerLibrarySelectionFooterTests({bindings, overlayBindings, actualButtonProps}: LibraryFooterBindings) {
  for (const failed of [false, true]) test(`actual Library selection result footer binds Done/Retry to the named TV panel (${failed ? 'failed' : 'saved'})`, async t => {
    t.mock.timers.enable({apis: ['setTimeout']});
    const b = await bindings(), o = await overlayBindings(b), selection = await import('../../../../packages/client-core/src/selection-model');
    const scope = await import('../../src-next/screens/destinations/lifetime'), display = await import('../../src-next/shell/selection-display');
    const module = await screenModule(new URL('../../src-next/shell/library-selection.tsx', import.meta.url), {
      '../platform/offline-downloads': {},
      react: b.react, 'react-native': b.native, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})},
      '../../../../packages/i18n/src': {defaultI18n}, '../../../../packages/client-core/src/presentation/errors': await import('../../../../packages/client-core/src/presentation/errors'),
      '../../../../packages/client-core/src/catalogue-query-selection': await import('../../../../packages/client-core/src/catalogue-query-selection'),
      '../../../../packages/client-core/src/selection-model': selection, '../../../../packages/client-core/src/presentation/selection-notice': {selectionDoneNotice: (args: unknown, failed = 0) => JSON.stringify([args, failed])}, '../../../../packages/client-core/src/media-selection': await import('../../../../packages/client-core/src/media-selection'),
      './selection-display': display, '../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'PhoneBar', BulkEditor: 'Bulk', DeleteMediaDialog: 'Delete'},
      '../kit/theme': {useTheme: () => b.theme}, '../kit/overlays': o.surface, '../kit/controls': {Button: o.Button},
      '../screens/destinations/lifetime': scope, '../screens/native-screen-services': {nativeScreenServices: () => ({})}, '../kit/selection/LoadedDestinationPicker': {NativeDestinationPicker: 'Picker'},
      '../screens/search/loaded-destinations': {NativeLoadedDestinations: class {}}, '../screens/destinations/bulk-media': {NativeBulkMedia: class {}}, '../screens/library/model': {},
      '../screens/library/selection-unavailable-actions': selectionUnavailableActions,
      '../screens/library/play-selection': {createNativeLibraryPlaySelection() {throw new Error('footer vector must not create a producer');}}});
    const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'original', viewerFence: 'viewer'};
    const context = {session: () => ({viewer: {...authority, authority: 'local'}}), authority: () => authority, current: () => true, subscribeAuthority: () => () => {}};
    const libraryListeners = new Set<() => void>(), librarySnapshot = {phase: 'loading'};
    const librarySource = {getSnapshot: () => librarySnapshot, subscribe: (listener: () => void) => {libraryListeners.add(listener); return () => libraryListeners.delete(listener);}};
    const port = module.createNativeLibrarySelection(context, librarySource, {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {visible: true}; return () => snapshot;})()});
    const element = port.render(); // Obtain the real private presenter through its public factory.
    let busy = false, token = 1, active = true, clears = 0, retries = 0, tree: any, panel: any;
    const view = {active: true, selectedKeys: ['original'], target: {mode: 'explicit'}, summary: {count: 1, types: [{kind: 'movie', count: 1}], every: {}}};
    // The selection operation port is controlled; all presenter guards and kit bindings are real.
    const store = {queryDeleteOffered: () => false, subscribe: () => () => {}, getSnapshot: () => 1, snapshot: () => view, active: () => active, personal: () => ({results: [{key: 'original', title: 'Original', status: failed ? 'failed' : 'saved'}]}),
      personalBusy: () => busy, busy: () => busy, resultsToken: () => token, whole: () => undefined, wholeUnavailable: () => undefined, destination: () => undefined, destinationKind: () => undefined, bulk: () => undefined,
      queueControl: () => false, loadedCount: () => 1, entries: () => [], clear() {clears++; active = false;}, async retryPersonal() {retries++;}};
    const source = createFabricHookHost(() => {tree = element.type({...element.props, store});});
    let renderer: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
    try {
      await b.region(() => ({id: 'original.library', order: ['opener']})); const opener = await b.target('original.library', () => ({focusId: 'opener'})); opener.props.onFocus({});
      await source.mount(); const props = () => nodes(tree).find(node => node.type === o.surface.SidePanel).props;
      assert.equal(props().testID, 'library-selection-results');
      renderer = createFabricHookHost(() => {panel = o.surface.SidePanel(props());}); await renderer.mount();
      const boundary = nodes(panel).find(node => node.type === o.surface.FocusBoundary), declaration = boundary.type(boundary.props), guide = await b.region(() => declaration.props);
      const footer = (id: string) => nodes(props().footer).find(node => node.type === o.Button && node.props.focusId === id).props;
      const done = await b.target('library-selection-results', () => actualButtonProps(o, footer('done')));
      const retry = failed ? await b.target('library-selection-results', () => actualButtonProps(o, footer('retry'))) : undefined;
      const targets = [done, ...(retry ? [retry] : [])], oldDone = footer('done').onPress, oldRetry = retry ? footer('retry').onPress : undefined;
      const refs = targets.map(target => target.props.ref);
      t.mock.timers.tick(180); assert.equal((retry ?? done).calls, 1, 'the existing initial ref chooses Retry only for actual failed results');
      assert.deepEqual(targets.map(target => target.props.testID), ['done', ...(failed ? ['retry'] : [])].map(id => registryModule.focusTestID('library-selection-results', id)));
      busy = true; await source.rerender(); await renderer.rerender(); for (const target of targets) await target.refresh();
      targets.forEach(target => {assert.equal(target.props.disabled, true); assert.equal(target.props.onPress, undefined);});
      assert.equal([...o.back][0]!(), true); assert.equal(clears, 0, 'busy results still own Back');
      oldDone(); oldRetry?.(); assert.equal(clears, 0); assert.equal(retries, 0);
      busy = false; token++; await source.rerender(); await renderer.rerender(); for (const target of targets) await target.refresh();
      targets.forEach((target, index) => {
        assert.equal(target.props.ref, refs[index]);
        assert.equal(b.registry.child({regionId: 'library-selection-results', childId: index ? 'retry' : 'done'}), target.host);
      });
      oldDone(); oldRetry?.(); assert.equal(clears, 0); assert.equal(retries, 0, 'retained old result callbacks still refuse a successor token');
      if (retry) {retry.props.onPress(); assert.equal(retries, 1); assert.equal([...o.back][0]!(), true);}
      else done.props.onPress();
      // The real parent removes these controls in this commit. Separate probe
      // roots must unmount their children before inspecting the retired parent.
      for (const target of targets) await target.dispose(); await guide.dispose();
      await source.rerender(); assert.equal(tree, null); await renderer.unmount();
      assert.equal(clears, 1); assert.equal(opener.calls, 1, 'actual panel teardown restores the original captured library opener');
    } finally {await renderer?.unmount(); await source.unmount(); port.dispose(); assert.equal(libraryListeners.size, 0); await b.dispose(); t.mock.timers.reset();}
  });
}
