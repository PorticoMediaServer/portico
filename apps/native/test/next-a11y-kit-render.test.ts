import test from 'node:test';
import assert from 'node:assert/strict';
import {buttonSpec, fieldSpec, focusSpec} from '@portico/design';
import {createTheme, fontFamilies, nativeType} from '../src-next/kit/theme.ts';
import * as controlModel from '../src-next/kit/controls/control-model.ts';
import * as cardModel from '../src-next/kit/cards/model.ts';
import {sessionViewerParts} from '../src-next/kit/cards/session-identity.ts';
import {hooks, liveOverlayPaint, screenModule} from './helpers/screen-harness.mjs';

// Execute the real render functions and command handlers against explicit native
// hosts. These are prop/behavior regressions, not measurements of native layout.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const textContent = (node: any): string => Array.isArray(node) ? node.map(textContent).join(' ') : node?.props ? textContent(node.props.children) : typeof node === 'string' ? node : '';
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
const idle = {pressed: false, focused: false, hovered: false};
const defaultI18n = {t: (key: string, values?: Record<string, unknown>) => values?.title ? `${key}: ${values.title}` : key};

function fixture(fontScale = 1, width = 390) {
  const host = hooks(), theme = createTheme({isTV: false, width, fontScale});
  const react = {...host.react, createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, ...(children.length ? {children: children.length === 1 ? children[0] : children} : {})}}),
    forwardRef: (render: any) => (props: any) => render(props, null), useId: () => 'field'};
  const native = {View: 'View', Text: 'Text', Pressable: 'Pressable', TextInput: 'TextInput', ScrollView: 'ScrollView', Modal: 'Modal', FlatList: 'FlatList', VirtualizedList: 'VirtualizedList',
    ActivityIndicator: 'ActivityIndicator', Platform: {OS: 'ios'}, StyleSheet: {flatten, create: (value: any) => value, absoluteFill: {position: 'absolute', inset: 0}}};
  const animated = {default: {View: 'AnimatedView', createAnimatedComponent: (component: any) => component},
    useSharedValue: (value: any) => react.useRef({value}).current, useAnimatedStyle: (read: () => any) => read(), withTiming: (value: any) => value};
  const state = {waiting: false, interactive: true};
  const common = {
    react: {...react, default: react}, 'react-native': native, 'react-native-reanimated': animated,
    '../theme': {useTheme: () => theme, nativeType, fontFamilies},
    '../useMotion': {useMotion: () => ({reduce: false, transforms: false, timing: () => ({duration: 0})})},
    '../focus': {useFocusTarget: (options: any) => ({waiting: state.waiting, testID: options.testID, ref: null, onFocus() {}, onBlur() {}}), useFocusWaiting: () => state.waiting, FocusRegion: 'FocusRegion'},
    '../dev-observation': {useNativeObservation: (_facts: unknown, ref: unknown) => ({ref, textProps: {}, recordFocus() {}, recordDeclaredStyle() {}})},
  };
  return {host, theme, state, common, native};
}

async function focusableFixture() {
  const f = fixture();
  const {Focusable} = await screenModule(new URL('../src-next/kit/controls/Focusable.tsx', import.meta.url), {
    ...f.common, '@portico/design': {focusSpec}, './control-model': controlModel, './useFocusedMenu': {useFocusedMenu() {}},
    '../overlays/OverlayPaintContext': {...liveOverlayPaint, useOverlayPaintMode: () => ({visible: true, interactive: f.state.interactive})},
  });
  return {...f, render: (props: any) => f.host.render(() => {const adapter = Focusable(props); return adapter.type(adapter.props);})};
}

test('Focusable exposes button role for a press, retains explicit roles, and leaves passive text unclassified', async () => {
  const f = await focusableFixture();
  assert.equal(f.render({onPress() {}}).props.accessibilityRole, 'button');
  assert.equal(f.render({onPress() {}, accessibilityRole: 'tab'}).props.accessibilityRole, 'tab');
  assert.equal(f.render({accessibilityRole: 'text'}).props.accessibilityRole, 'text');
  assert.equal(f.render({}).props.accessibilityRole, undefined);
});

test('a named long-press action merges once and dispatches through the same fenced handler', async () => {
  const f = await focusableFixture(), calls: string[] = [];
  const props = {onPress() {}, onLongPressAction: () => calls.push('download'), longPressAccessibilityAction: {name: 'download', label: 'Download'},
    accessibilityActions: [{name: 'more', label: 'More'}, {name: 'download', label: 'Old label'}],
    onAccessibilityAction: (event: any) => calls.push(event.nativeEvent.actionName)};
  let tree = f.render(props);
  assert.deepEqual(tree.props.accessibilityActions, [{name: 'more', label: 'More'}, {name: 'download', label: 'Download'}]);
  tree.props.onAccessibilityAction({nativeEvent: {actionName: 'download'}});
  tree.props.onAccessibilityAction({nativeEvent: {actionName: 'more'}});
  tree.props.onLongPress({});
  assert.deepEqual(calls, ['download', 'more', 'download']);
  f.state.waiting = true; tree = f.render(props);
  assert.deepEqual(tree.props.accessibilityActions, [{name: 'more', label: 'More'}]);
  tree.props.onAccessibilityAction({nativeEvent: {actionName: 'download'}});
  assert.equal(calls.length, 3, 'an unloaded TV host cannot dispatch its long-press command');
  assert.equal(f.render({...props, disabled: true}).props.onAccessibilityAction, undefined);
  f.state.interactive = false;
  assert.equal(f.render(props).props.onAccessibilityAction, undefined, 'retained overlay paint has no action path');
});

test('reading-card long press opens the item menu (which offers Download) and its author is part of the spoken identity', async () => {
  // MOB-055 replaced the card's bare long-press Download with the standard
  // reading menu, so no unnamed long-press side effect remains (NM-08).
  const f = fixture(), menus: unknown[] = [];
  const {PhoneReadingBookCard} = await screenModule(new URL('../src-next/screens/read/PhoneReadingBookCard.tsx', import.meta.url), {
    react: f.common.react, 'react-native': f.native, '@portico/design': {}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {GeneratedCover: 'GeneratedCover', MediaArt: 'MediaArt'}, '../../kit/controls': {Focusable: 'Focusable'},
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => f.theme}, '../../kit/cards/model': cardModel,
  });
  const props = {testID: 'book', title: 'The same title', author: 'Author A', width: 110, onOpen() {}};
  const card = f.host.render(() => PhoneReadingBookCard({...props, onMenu: (anchor: unknown) => {menus.push(anchor);}}));
  assert.equal(card.props.accessibilityLabel, 'The same title, Author A');
  card.props.onLongPressAction(); assert.equal(menus.length, 1, 'long press opens the anchored item menu');
  assert.equal(card.props.longPressAccessibilityAction, undefined);
  assert.equal(f.host.render(() => PhoneReadingBookCard(props)).props.onLongPressAction, undefined);
});

test('text buttons keep authored default geometry and grow/wrap only when text is enlarged', async () => {
  for (const scale of [1, 2.5]) {
    const f = fixture(scale);
    const {Button} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {
      ...f.common, '@portico/design': {buttonSpec}, './Focusable': {Focusable: 'Focusable'}, './control-model': controlModel, '../Icon': {Icon: 'Icon'},
      '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})},
    });
    const outer = f.host.render(() => Button({size: 'xs', children: 'A longer action label', style: {height: 28, minHeight: 32}}));
    const button = f.host.render(() => outer.type(outer.props));
    const style = flatten(button.props.style(idle)), content = button.props.children(idle), label = nodes(content).find(node => node.type === 'Text');
    assert.equal(style.height, scale === 1 ? 28 : undefined);
    assert.equal(style.minHeight, 32, 'a caller minimum is retained');
    assert.equal(label.props.numberOfLines, scale === 1 ? 1 : undefined);
    const iconOuter = f.host.render(() => Button({variant: 'icon', size: 'xs', iconOnly: true, icon: 'i-more'}));
    const icon = f.host.render(() => iconOuter.type(iconOuter.props));
    assert.equal(flatten(icon.props.style(idle)).height, 34, 'decorative glyph geometry does not scale');
  }
});

test('single-line fields keep text scaling enabled and release the fixed parent height at large text', async () => {
  for (const scale of [1, 2.5]) {
    const f = fixture(scale);
    const {TextField} = await screenModule(new URL('../src-next/kit/controls/TextField.tsx', import.meta.url), {
      ...f.common, '@portico/design': {fieldSpec}, './control-model': controlModel, '../Icon': {Icon: 'Icon'},
      '../overlays/OverlayPaintContext': liveOverlayPaint, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})},
    });
    const tree = f.host.render(() => TextField({value: 'Editable text', fieldStyle: {height: 28, minHeight: 35}}));
    const field = nodes(tree).find(node => node.type === 'View' && flatten(node.props.style).borderRadius !== undefined);
    const input = nodes(tree).find(node => node.type === 'TextInput');
    assert.equal(flatten(field.props.style).height, scale === 1 ? 28 : undefined);
    assert.equal(flatten(field.props.style).minHeight, 35);
    assert.equal(input.props.multiline, undefined);
    assert.notEqual(input.props.allowFontScaling, false);
    assert.equal(input.props.maxFontSizeMultiplier, undefined);
  }
});

test('Select retains one list scroller and matches its native option pitch to OS text scale', async () => {
  for (const scale of [1, 2.5]) {
    const f = fixture(scale);
    const {Select} = await screenModule(new URL('../src-next/kit/controls/Select.tsx', import.meta.url), {
      ...f.common, '../../../../../packages/i18n/src/index': {defaultI18n}, '../Icon': {Icon: 'Icon'}, './Focusable': {Focusable: 'Focusable'},
      '../overlays/surface': {Dialog: 'Dialog'}, './control-model': controlModel,
      './useControlValue': {useControlValue: (value: any, initial: any) => [value ?? initial, () => {}]},
    });
    const outer = f.host.render(() => Select({open: true, options: [{value: 'first', label: 'First'}, {value: 'second', label: 'Second'}]}));
    const tree = f.host.render(() => outer.type(outer.props)), all = nodes(tree);
    assert.equal(all.find(node => node.type === 'Dialog').props.scrollable, false);
    const list = all.find(node => node.type === 'FlatList');
    const row = list.props.renderItem({item: {value: 'second', label: 'Second'}, index: 1});
    assert.deepEqual(list.props.getItemLayout(undefined, 1), {length: 52 * scale, offset: 52 * scale, index: 1});
    assert.equal(flatten(row.props.style).height, 52 * scale);
  }
});

test('Dialog provides bounded scrolling by default and honors a body-owned list scroller', async () => {
  for (const width of [390, 800]) for (const scrollable of [undefined, false]) {
    const f = fixture(1, width);
    const surface = await screenModule(new URL('../src-next/kit/overlays/surface.tsx', import.meta.url), {
      ...f.common, './OverlayPaintContext': liveOverlayPaint, 'react-native-svg': {default: 'Svg'},
      '../../platform/layout/tv-canvas': {}, './TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'},
      '../../../../../packages/i18n/src/index': {defaultI18n}, '../controls/Button': {Button: 'Button'}, '../controls/Focusable': {Focusable: 'Focusable'},
      '../Icon': {Icon: 'Icon'}, './BottomSheet': {BottomSheet: 'BottomSheet'},
      './OverlayHost': {useOverlayLifecycle() {}, OverlayPortal: 'OverlayPortal'},
    });
    const props = {open: true, title: 'Choices', onOpenChange() {}, children: 'Long body', footer: 'Footer', scrollable};
    const outer = f.host.render(() => surface.Dialog(props));
    if (width === 390) {
      assert.equal(outer.type, 'BottomSheet'); assert.equal(outer.props.fitContent, true);
      assert.equal(outer.props.scrollable, scrollable !== false);
      assert.equal(outer.props.keyboardAvoiding, undefined, "forms keep their own keyboard padding");
    } else {
      const tree = f.host.render(() => outer.type(outer.props)), scrolls = nodes(tree).filter(node => node.type === 'ScrollView');
      assert.equal(scrolls.length, scrollable === false ? 0 : 1);
      if (scrolls.length) {
        assert.equal(flatten(scrolls[0].props.style).flexShrink, 1);
        assert.equal(flatten(scrolls[0].props.style).minHeight, 0);
        assert.equal(scrolls[0].props.keyboardShouldPersistTaps, 'handled');
        assert.ok(!textContent(scrolls[0]).includes('Footer'), 'footer stays outside the body scroller');
      }
    }
  }
});

test('song rows speak their artist/album/state and enlarged rows release fixed virtualization estimates', async () => {
  for (const scale of [1, 2.5]) {
    const f = fixture(scale), calls: string[] = [];
    const tracks = await screenModule(new URL('../src-next/kit/browse/Tracks.tsx', import.meta.url), {
      ...f.common, '../../../../../packages/i18n/src': {defaultI18n}, '../controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'},
      '../Icon': {Icon: 'Icon'}, '../cards/primitives': {CardFrame: 'CardFrame'}, '../cards/model': cardModel,
      './presentation': {alpha: (value: string) => value, browseType: () => ({})},
    });
    const row = {id: 'song', text: {title: 'Song', caption: [{id: 'artist', text: 'Artist'}, {id: 'album', text: 'Album'}], detail: []}, duration: {id: 'duration', text: '3:32'}, playing: true};
    const song = f.host.render(() => tracks.SongRow({row, onPlay: (id: string) => calls.push(id), onEnter() {}}));
    const frame = nodes(song).find(node => node.type === 'CardFrame');
    assert.equal(frame.props.accessibilityLabel, 'next.browse.native.play: Song, Artist, Album, 3:32, next.kit.session.playing');
    frame.props.onPlay(); assert.deepEqual(calls, ['song']);
    assert.equal(flatten(song.props.style).height, scale === 1 ? 58 : undefined);
    assert.equal(flatten(song.props.style).minHeight, 58);
    const snapshot = {generation: 1, total: 1}, collection = {subscribe: () => () => {}, getSnapshot: () => snapshot};
    const table = f.host.render(() => tracks.TrackTable({variant: 'songs', collection, selection: {}, onPlay() {}}));
    const list = f.host.render(() => table.type(table.props));
    assert.equal(list.type, 'VirtualizedList');
    assert.equal(typeof list.props.getItemLayout, scale === 1 ? 'function' : 'undefined');
  }
});

test('CardFrame announces presentation context and state while preserving named actions and overrides', async () => {
  const f = fixture(), calls: string[] = [];
  const {CardFrame} = await screenModule(new URL('../src-next/kit/cards/primitives.tsx', import.meta.url), {
    ...f.common, 'react-native-svg': {default: 'Svg'}, '../../../../../packages/i18n/src/index': {defaultI18n}, './model': cardModel,
    '../browse/useTouchHold': {useTouchHold: () => ({phase: 'idle', consumePress: () => false, end() {}, cancel() {}, handlers: {onTouchStart() {}, onTouchMove() {}, onTouchEnd() {}, onTouchCancel() {}}})},
    '../../platform/haptics': {}, '../../platform/menu-key': {registerFocusedMenu: () => () => {}},
    '../Icon': {Icon: 'Icon'}, '../selection/index': {SelectionHoldFeedback: 'SelectionHoldFeedback'},
  });
  const props = {title: 'Title', caption: [{id: 'year', text: '1982'}], detail: [{id: 'duration', text: '109 min'}], watched: true, isNew: true, progress: .42,
    onNavigate() {}, onPlay: () => calls.push('play'), onMore: () => calls.push('more'),
    selection: {active: false, selected: false, onToggle() {}, onEnter: () => calls.push('select')}, children: () => null};
  const render = (extra: any = {}) => nodes(f.host.render(() => CardFrame({...props, ...extra}))).find(node => node.type === 'Pressable');
  let card = render();
  assert.equal(card.props.accessibilityLabel, 'Title, 1982, 109 min, card.watched, next.kit.new');
  assert.deepEqual(card.props.accessibilityValue, {min: 0, max: 100, now: 42});
  for (const actionName of ['play', 'more', 'select']) card.props.onAccessibilityAction({nativeEvent: {actionName}});
  assert.deepEqual(calls, ['play', 'more', 'select']);
  assert.equal(render({accessibilityLabel: 'Supplied full label'}).props.accessibilityLabel, 'Supplied full label');
  f.state.waiting = true; card = render();
  assert.equal(card.props.accessibilityLabel, 'next.selection.native.loading');
  assert.deepEqual(card.props.accessibilityActions, []);
  assert.equal(card.props.accessibilityValue, undefined);
  card.props.onAccessibilityAction({nativeEvent: {actionName: 'play'}});
  assert.equal(calls.length, 3);
});

test('specialized cards forward episode context and the same viewer/session facts as their visible face', async () => {
  const f = fixture();
  const media = await screenModule(new URL('../src-next/kit/cards/MediaCards.tsx', import.meta.url), {
    ...f.common, 'react-native-svg': {default: 'Svg'}, '../../../../../packages/i18n/src/index': {defaultI18n},
    '../artwork/index': {}, '../controls/Focusable': {Focusable: 'Focusable'}, '../selection/index': {SelectionCircle: 'SelectionCircle'},
    './primitives': {CardFrame: 'CardFrame', Parts: 'Parts', titleStyle: () => ({})},
  });
  const episode = f.host.render(() => media.EpisodeTile({title: 'Pilot', label: [{id: 'episode', text: 'S1 E2'}, {id: 'show', text: 'Show'}],
    length: '43 min', status: 'New episode', layout: 'row', onNavigate() {}}));
  assert.deepEqual(episode.props.accessibilityDetails, ['S1 E2', 'Show', 'New episode', '43 min']);
  const listening = await screenModule(new URL('../src-next/kit/cards/ListeningCards.tsx', import.meta.url), {
    ...f.common, '../../../../../packages/i18n/src/index': {defaultI18n}, '../artwork/index': {}, '../controls/index': {Button: 'Button'},
    './MediaCards': {CardOverlays: 'CardOverlays'}, './primitives': {CardFrame: 'CardFrame'}, './session-identity': {sessionViewerParts},
  });
  const session = f.host.render(() => listening.SessionCard({title: 'Pilot', identity: [{id: 'episode', text: 'S1 E2'}],
    facts: [{id: 'account', text: 'Old identity'}, {id: 'device', text: 'Old device'}, {id: 'quality', text: '1080p'}, {id: 'position', text: '20:00 / 43:00'}],
    viewer: {name: 'Evening', username: 'owner', device: 'Bedroom TV'}, paused: true, onNavigate() {}}));
  assert.deepEqual(session.props.accessibilityDetails, ['S1 E2', 'owner', 'Evening', 'Bedroom TV', '1080p', '20:00 / 43:00', 'next.kit.session.paused']);
});
