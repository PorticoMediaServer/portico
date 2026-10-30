import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {applyNativeTVKeyboardKey, nativeTVKeyboardRows} from '../src-next/screens/search/keyboard.ts';

const origin = new URL('../src-next/screens/library/tv-library/', import.meta.url);
const searchOrigin = new URL('../src-next/screens/search/', import.meta.url);
const authorityKey = (scope: any) => JSON.stringify([scope.serverId, scope.accountId, scope.profileId, scope.epoch, scope.viewerFence]);
const keys = [...nativeTVKeyboardRows.flat(), 'space', 'delete', 'clear'] as const;

function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node?.props ? [node, ...elements(node.props.children)] : [];
}
/** createElement collects children into an array, so a single run reads as [text]. */
function textIs(node: any, value: string): boolean {
  if (node?.type !== 'Text') return false;
  const children = Array.isArray(node.props.children) ? node.props.children : [node.props.children];
  return children.length === 1 && children[0] === value;
}

/** A genuine-shape naming opening: one controller, one borrowed input, and the
 * real close/dispose order the renderer owns. */
function namingFixture(args: any[]) {
  let form: any = {open: true, name: 'Genre Action', busy: false, nameLocked: false};
  let active = true, revision = 0, disposed = 0, closes = 0;
  const listeners = new Set<() => void>();
  const emit = () => {for (const listener of [...listeners]) listener();};
  const controller = {
    getSnapshot: () => form,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    setName: (name: string) => {if (!active || form.busy || form.nameLocked) return; form = {...form, name}; emit();},
    save: async () => {},
    dispose: () => {if (!active) return; active = false; disposed++; form = {open: false, name: '', busy: false, nameLocked: false}; emit();},
    close: () => {if (form.busy) return false; if (active) {active = false; disposed++; closes++; form = {open: false, name: '', busy: false, nameLocked: false}; emit();} return true;},
  };
  let keyboardActive = false, opens = 0;
  const input = {
    // The reviewed read(): one opening ticket, re-renewed only by an explicit
    // keyboard opening, never by a text change.
    getSnapshot: () => ({form, keyboard: {visible: keyboardActive && form.open && !form.busy && !form.nameLocked, intent: 'enter' as const, revision}}),
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    openKeyboard: () => {
      if (!form.open || form.busy || form.nameLocked) return false;
      if (!keyboardActive) {keyboardActive = true; revision++; emit();}
      opens++;
      return true;
    },
    press: (key: string) => {
      const state = input.getSnapshot() as any;
      if (!state.keyboard.visible) return false;
      const next = applyNativeTVKeyboardKey(state.form.name, key as any);
      if (next === state.form.name || Array.from(next).length > 200) return false;
      controller.setName(next);
      return true;
    },
    back: () => 'close' as const,
    dispose: () => {},
  };
  const naming = Object.freeze({controller, input, close: controller.close, dispose: controller.dispose});
  return {
    args, naming, controller, input, close: controller.close, dispose: controller.dispose,
    get form() {return form;}, setForm: (next: any) => {form = next; emit();},
    get disposed() {return disposed;}, get closes() {return closes;}, get opens() {return opens;},
  };
}

async function namingModule() {
  const created: any[] = [];
  const mod = await screenModule(new URL('naming.ts', origin), {
    react: {default: {}},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../destinations/focus': {},
    '../../../shell/bootstrap': {},
    '../../../shell/model': {authorityKey},
    '../../../tv/library/save-view': {createNativeTVLibraryNaming: (...args: any[]) => {const fixture = namingFixture(args); created.push(fixture); return fixture.naming;}},
    '../LibraryScreen': {},
    '../model': {},
    '../save-view': {},
  });
  return {...mod, created};
}

function contextFixture() {
  const navigationListeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  let alive = true;
  const context = {
    current: () => alive,
    navigation: {subscribe: (listener: () => void) => {navigationListeners.add(listener); return () => {navigationListeners.delete(listener);};}},
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
  };
  return {context: context as any, navigate: () => {for (const listener of [...navigationListeners]) listener();},
    changeAuthority: () => {for (const listener of [...authorityListeners]) listener();}, retire: () => {alive = false;}};
}

const destination = (id: string) => ({getSnapshot: () => ({library: {id}})} as any);
const draft = {predicates: [{field: 'genre', operator: 'contains-any', value: ['Action']}], sort: {field: 'title', direction: 'asc'}} as any;

const screenStubs = (host: any, naming: any) => ({
  react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../../packages/i18n/src': {defaultI18n},
  '../../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../../kit/controls': {Button: 'Button'},
  '../../../kit/focus': {useFocusRegistry: () => ({focus: () => true})}, '../../../kit/overlays': {Banner: 'Banner', SidePanel: 'SidePanel'},
  '../../../kit/browse/presentation': {alpha: () => '#000', browseType: () => ({})},
  '../../../kit/theme': {useTheme: () => ({colors: {text: '#fff', textTertiary: '#aaa', accentStrong: '#5cb3e2'}})},
  '../../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'}, './naming': naming,
});

test('F-LIB-52 opens the naming keyboard on the shared keyboard own first key', async () => {
  const naming = await namingModule();
  assert.deepEqual(naming.nativeTVNamingFirstKey('panel.keyboard'), {regionId: 'panel.keyboard.row.0', childId: 'a'});
  const entered: any[] = [], host = hooks();
  const {NativeTVKeyboard} = await screenModule(new URL('TVKeyboard.tsx', searchOrigin), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {alpha: () => '#000', browseType: () => ({})},
    '../../kit/theme': {useTheme: () => ({colors: {}, isTV: true})},
    '../destinations/focus': {}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (activation: any, address: any) => entered.push({activation, address})},
    './keyboard': {applyNativeTVKeyboardKey, nativeTVKeyboardRows},
  });
  const props = {id: 'panel.keyboard', value: '', onChangeText: () => {}, activation: {visible: true, intent: 'enter' as const, revision: 1}, leftRegionId: 'panel.keyboard', width: 500};
  host.render(() => NativeTVKeyboard(props));
  assert.equal(entered.length, 1);
  assert.deepEqual(entered[0].address, naming.nativeTVNamingFirstKey('panel.keyboard'), 'the panel and the keyboard agree on the entry key');
  const row = elements(host.render(() => NativeTVKeyboard(props))).filter(node => node.props.id === 'panel.keyboard.row.0');
  assert.equal(row.length, 1);
  assert.equal(elements(row[0]).find(node => node.type === 'Focusable').props.focusId, 'a', 'row 0 starts on a');
});

test('the typed name obeys the same rule as the reviewed key press', async () => {
  const {nativeTVNameEdit} = await namingModule();
  const open = {visible: true, intent: 'enter' as const, revision: 1};
  for (const key of keys) {
    const form = {open: true, name: 'Genre Action', busy: false, nameLocked: false};
    const expected = applyNativeTVKeyboardKey(form.name, key as any);
    assert.equal(nativeTVNameEdit(form, open, expected), expected, `key ${key} types through the panel`);
  }
  assert.equal(nativeTVNameEdit({open: true, name: 'Genre Action', busy: false, nameLocked: false}, open, 'Genre Action'), undefined, 'no-op edits are refused');
  assert.equal(nativeTVNameEdit({open: true, name: 'x'.repeat(200), busy: false, nameLocked: false}, open, 'x'.repeat(201)), undefined, 'the server name bound is enforced');
  assert.equal(nativeTVNameEdit({open: true, name: 'x'.repeat(199), busy: false, nameLocked: false}, open, 'x'.repeat(200)), 'x'.repeat(200));
  assert.equal(nativeTVNameEdit({open: true, name: 'Name', busy: true, nameLocked: false}, open, 'Name2'), undefined, 'a pending receipt is not edited');
  assert.equal(nativeTVNameEdit({open: true, name: 'Name', busy: false, nameLocked: true}, open, 'Name2'), undefined, 'a locked conflicted name is not edited');
  assert.equal(nativeTVNameEdit({open: true, name: 'Name', busy: false, nameLocked: false}, {...open, visible: false}, 'Name2'), undefined, 'a closed keyboard types nothing');
});

test('one Save view opening at a time, retired by the panel close or the real fence', async () => {
  const naming = await namingModule(), {context, navigate, changeAuthority, retire} = contextFixture();
  const store = naming.createNativeTVLibraryNamingStore(context);
  const seen: any[] = [];
  store.subscribe(() => seen.push(store.getSnapshot()));
  assert.equal(store.getSnapshot(), undefined);
  assert.equal(store.open(destination('movies'), draft), true);
  assert.equal(naming.created.length, 1);
  const first = naming.created[0];
  assert.equal(first.args[0], context, 'the opening borrows the real bootstrap context');
  assert.equal(first.args[3], defaultI18n, 'ruling 38 names come from the UI language');
  assert.equal(store.getSnapshot(), first.naming);
  assert.equal(first.opens, 1, 'the drawn opening already holds its first key');
  assert.equal(first.input.getSnapshot().keyboard.visible, true);

  // An accepted save owns its receipt: a second press cannot start over.
  first.setForm({open: true, name: 'Genre Action', busy: true, nameLocked: false});
  assert.equal(store.open(destination('movies'), draft), false);
  assert.equal(naming.created.length, 1);
  first.setForm({open: true, name: 'Genre Action', busy: false, nameLocked: false});

  // Close keeps the drawn surface mounted so its exit motion finishes.
  assert.equal(store.close(), true);
  assert.equal(first.closes, 1);
  assert.equal(store.getSnapshot(), first.naming, 'the closing surface still renders its exit');

  // The next opening replaces the closed one without stacking.
  assert.equal(store.open(destination('shows'), draft), true);
  assert.equal(first.disposed, 1, 'a replaced opening releases its captured definition');
  assert.equal(naming.created.length, 2);
  const second = naming.created[1];

  // The borrowed form disposes itself on a route change; the store stops
  // handing out a retired opening.
  second.naming.controller.dispose();
  assert.equal(store.getSnapshot(), second.naming, 'the store still holds the closing opening');
  navigate();
  assert.equal(store.getSnapshot(), undefined, 'leaving the library retires the name keyboard');
  changeAuthority();
  assert.equal(store.getSnapshot(), undefined);
  store.dispose();
  retire();
  assert.equal(store.getSnapshot(), undefined);
  assert.ok(seen.length >= 3);
});

test('the TV toolbar Save view opens the keyboard and never the phone sheet', async () => {
  const naming = await namingModule(), {context} = contextFixture();
  const store = naming.createNativeTVLibraryNamingStore(context);
  const calls: string[] = [];
  const movies = destination('movies'), shows = destination('shows');
  const actions = {dispose: () => calls.push('dispose'), open: () => calls.push('open'), play: () => calls.push('play'),
    menu: () => calls.push('menu'), saveView: (actual: any) => calls.push(actual === movies ? 'phone-movies' : 'phone-shows')};
  const tv = naming.nativeTVLibraryActions(actions, store, movies);
  assert.equal(tv.open, actions.open, 'every other library action is the caller own');
  assert.equal(tv.dispose, actions.dispose);
  tv.saveView(movies, draft);
  assert.equal(naming.created.length, 1);
  assert.deepEqual(calls, [], 'the phone naming sheet is not reached on TV');
  tv.saveView(shows, draft);
  assert.equal(naming.created.length, 1, 'a stale destination never redirects another library opening');
  assert.deepEqual(calls, []);
  store.dispose();
});

test('ruling 37 keeps today fields and strings on the 680px TV side surface', async () => {
  const naming = await namingModule(), host = hooks();
  const {NativeTVLibrarySaveViewPanel, NativeTVLibrarySurface} = await screenModule(new URL('TVLibraryScreen.tsx', origin), screenStubs(host, naming));
  const fixture = namingFixture([]);
  fixture.input.openKeyboard();
  const render = () => host.render(() => NativeTVLibrarySaveViewPanel({naming: fixture.naming as any, id: 'panel'}));
  let tree = render();
  assert.equal(tree.type, 'SidePanel');
  assert.equal(tree.props.open, true);
  assert.equal(tree.props.testID, 'panel');
  assert.equal(tree.props.width, 680, 'the TV library filter panel width');
  assert.equal(tree.props.title, defaultI18n.t('lib.saveViewTitle'));
  assert.equal(tree.props.description, defaultI18n.t('lib.saveViewLede'));
  assert.equal(tree.props.showClose, false);
  let content = elements(tree);
  assert.ok(content.some(node => textIs(node, defaultI18n.t('lib.saveViewName'))), 'the Name label is today catalogue copy');
  let field = content.find(node => node.props.testID === 'panel.field');
  assert.equal(field.props.style[0].width, 500, 'the drawn 500-wide field (tv.html frame 5)');
  assert.equal(field.props.style[0].height, 80);
  assert.ok(elements(field).some(node => node.props.testID === 'panel.field-caret'), 'the drawn caret is present (ruling 27.2)');
  assert.ok(elements(field).some(node => textIs(node, 'Genre Action')));
  let keyboard = content.find(node => node.type === 'NativeTVKeyboard');
  assert.equal(keyboard.props.id, 'panel.keyboard');
  assert.equal(keyboard.props.width, 500);
  assert.equal(keyboard.props.leftRegionId, 'panel.keyboard');
  assert.deepEqual(keyboard.props.activation, fixture.input.getSnapshot().keyboard);
  assert.equal(keyboard.props.value, 'Genre Action', 'ruling 37.6 starts from the §38 conditions suggestion');
  assert.ok(content.some(node => textIs(node, defaultI18n.t('lib.saveViewHelp'))));
  let buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons.length, 2);
  assert.equal(buttons[0].props.variant, 'glass'); assert.equal(buttons[1].props.variant, 'primary');
  assert.equal(buttons[0].props.disabled, false); assert.equal(buttons[1].props.disabled, false);

  // Typing goes through the panel rule and lands on the real controller.
  keyboard.props.onChangeText('Genre Action, Year 1990s');
  assert.equal(fixture.form.name, 'Genre Action, Year 1990s');

  // A real save is one controller call; Cancel closes the same opening.
  let saves = 0; fixture.controller.save = async () => {saves++;};
  buttons[1].props.onPress(); assert.equal(saves, 1);
  buttons[0].props.onPress(); assert.equal(fixture.closes, 1);
  fixture.setForm({open: true, name: 'Genre Action', busy: false, nameLocked: false});

  // Empty, busy and conflicted states keep today's guards.
  fixture.setForm({open: true, name: '   ', busy: false, nameLocked: false});
  tree = render(); buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons[1].props.disabled, true, 'a blank trimmed name cannot save');
  fixture.setForm({open: true, name: 'Kept name', busy: true, nameLocked: false});
  tree = render(); buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons[1].props.loading, true); assert.equal(buttons[1].props.disabled, true); assert.equal(buttons[0].props.disabled, true);
  assert.equal(tree.props.dismissOnBack, false, 'an accepted save is not dismissed by Back');
  fixture.setForm({open: true, name: 'Kept name', busy: false, nameLocked: true});
  tree = render(); buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons[1].props.disabled, true, 'a locked conflicted name waits for its receipt');
  assert.equal(tree.props.dismissOnBack, true, 'Back returns to Save view once nothing is in flight');

  // The error notice sits above the field, in today's copy and placement.
  fixture.setForm({open: true, name: 'Kept name', busy: false, nameLocked: false, error: {code: 'network_error'}});
  tree = render(); content = elements(tree);
  const banner = content.find(node => node.type === 'Banner');
  assert.equal(banner.props.variant, 'notice'); assert.equal(banner.props.tone, 'error'); assert.equal(banner.props.placement, 'inline');
  assert.equal(banner.props.message, presentError({code: 'network_error'}, 'library', {operation: 'save'}).body);
  assert.ok(content.indexOf(banner) < content.indexOf(field = content.find(node => node.props.testID === 'panel.field')));

  // The surface renders the real library page plus exactly one opening.
  let held: any;
  const store = {subscribe: () => () => {}, getSnapshot: () => held};
  let surface = host.render(() => NativeTVLibrarySurface({library: 'LibraryPage', naming: store as any, panelId: 'panel'}));
  assert.equal(surface.props.children[0], 'LibraryPage', 'the library page is never replaced');
  assert.equal(elements(surface).filter(node => node.type === NativeTVLibrarySaveViewPanel).length, 0);
  held = fixture.naming;
  surface = host.render(() => NativeTVLibrarySurface({library: 'LibraryPage', naming: store as any, panelId: 'panel'}));
  assert.equal(elements(surface).filter(node => node.type === NativeTVLibrarySaveViewPanel).length, 1, 'exactly one naming surface is mounted');
});
