import * as homeWindow from '../src-next/screens/home/home-window';
import {homeContinuationCard, homeContinuationSource} from '../../../packages/client-core/src/presentation/home-continuation';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry.ts';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import {heroSpec} from '@portico/design';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {authorityKey} from '../src-next/shell/model';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {tvFocusIds, tvHomeRegionMap} from '../src-next/shell/tv-focus';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';
import * as homeStart from '../src-next/screens/home/home-start';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {expandFrames} from './helpers/browse-frame-stub.mjs';
import {expandHome, withHomeCards} from './helpers/home-cards-stub.mjs';

const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];

test('initial TV focus reanchors when the compact hero measurement arrives and ignores inactive or foreign layout', async () => {
  const registry = createFocusRegistry();
  const h = hooks(), frames: FrameRequestCallback[] = [], anchors: number[][] = [];
  const previousFrame = globalThis.requestAnimationFrame;
  globalThis.requestAnimationFrame = callback => {frames.push(callback); return frames.length;};
  try {
    const entry = {id: 'episode', title: 'Actual episode', kind: 'episode', libraryId: 'tv',
      homeActions: {play: true, myList: true, details: true, state: {watchlisted: false}}};
    const row = {id: 'continue', kind: 'continue', artworkShape: 'landscape', entries: [entry]};
    const authority = {serverId: 'actual', viewerFence: 'viewer'};
    const document = {rows: [row], spotlight: [], serverId: authority.serverId, viewerFence: authority.viewerFence};
    let currentAuthority = authority;
    let anchorOptions: unknown[] = [];
    const vertical = {scrollRef: {}, offset: {value: 146}, repeating: false, stop() {}, move() {}, anchor: (...args: number[]) => anchors.push(args)};
    const runtime = {home: {subscribe() {}, getSnapshot: () => ({document, phase: 'ready'})}, playback: {},
      getSnapshot: () => ({authority: currentAuthority, libraries: [], selectionActive: false}), selection: () => ({}), text: {t: (key: string) => key, locale: 'en-US'}};
    const {HomeScreen} = expandHome(await screenModule(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), await withHomeCards({'../../shell/PlayOnButton': {PlayOnButton: 'PlayOnButton'}, '../../kit/layout/TopBar': {roundBarSize: 38}, './generated-poster': {generatedPoster: () => null}, './live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, '../../kit/overlays': {Banner: 'Banner'}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
      react: {...h.react, useLayoutEffect: h.react.useEffect}, '@portico/design': {heroSpec},
      'react-native': {AppState: {currentState: 'active'}, RefreshControl: 'RefreshControl', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
      'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => h.react.useRef({value}).current},
      '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
      '../../../../../packages/client-core/src/presentation/home-continuation': {homeContinuationCard, homeContinuationSource},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {homeRowPresentation: () => ({cards: [{entry}], title: 'Continue'}), homeSpotlightModels: () => [], homeTitleFactParts: () => []},
      '../../../../../packages/client-core/src/presentation/continue-books': {continueBookFacts: () => ({reading: false, shape: 'audiobook'})},
      '../../../../../packages/client-core/src/home-selection': {homeEntryCapabilities: () => ({playable: true, details: true, watchlisted: false})},
      '../../../../../packages/client-core/src/presentation/errors': {presentError},
      '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_: unknown, text: string) => text},
      '../../../../../packages/client-core/src/presentation/kit-model': {},
      '../../platform/ArtworkPipeline': {}, '../../kit/artwork': {}, '../../kit/controls': {Button: 'Button'}, '../../kit/cards': {wideDetailHeight: () => 0},
      '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
      '../../kit/focus/initial-entry': {createInitialRegionEntry},
      '../../shell/tv-focus': {tvFocusIds, tvHomeRegionMap}, './tv-presentation': {tvHomeRows}, '../../shell/home-paging': {NativeHomePaging}, './home-window': homeWindow, './home-start': homeStart,
      '../../kit/layout': {Row: 'Row', Hero: 'Hero', FocusPreviewHero: 'FocusPreviewHero'}, '../../kit/frames': {BrowseFrame: 'BrowseFrame'},
      '../../kit/layout/FocusAnchor': {useFocusAnchor: (...args: unknown[]) => {anchorOptions = args; return vertical;}}, '../../kit/layout/types': {}, '../../kit/states': {LoadingPaint: 'LoadingPaint', },
      '../../platform/home-action-diagnostics': {homeActionDiagnostics}, '../../kit/theme': {nativeType, useTheme: () => createTheme({isTV: true, width: 1920})}, '../../shell/model': {authorityKey, sameAuthority: (a: unknown, b: unknown) => a === b},
      '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../destinations/useTVPageBack': {useTVPageBack() {}},
      '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'}, './TvHomeRecovery': {TvHomeRecovery: 'Recovery'}, './useTvHomeRecovery': {useTvHomeRecovery: () => ({visible: false})},
      './HomeHeroFacts': {}, './model': {authorizedHome: (value: unknown) => value, platformRows: () => [row], homeRowHasContent: () => true, initialHomeFocus: () => ({rowId: row.id, entryId: entry.id}), homeCardText: () => ({title: entry.title}), homePlayTarget: () => entry},
    })));
    const render = (props = {}) => expandFrames(h.render(() => HomeScreen({runtime, authority, ...props})));
    const rowBox = (tree: any) => descendants(tree).find(node => node.type === 'TVHomeRow');
    const rowsBox = (tree: any) => descendants(tree).find(node => node.type === 'BrowseFrameRows');
    const layout = (y: number, height = 300) => ({nativeEvent: {layout: {y, height}}});
    let tree = render();
    const hero = descendants(tree).find(node => node.type === 'Hero');
    const candidate = {entry, target: {...entry, playback: {itemId: entry.id}}};
    // Owner, 18 Nov: TV Home opens on the first Continue Watching card; the spotlight has no buttons, and the first row
    // stops Up.
    assert.equal(hero.props.renderActions(candidate), null, 'the TV spotlight has no buttons');
    const renderedRow = descendants(tree).find(node => node.type === 'Row');
    assert.equal(renderedRow.props.focus.stops?.up, true, 'Up from the first row stays in it');
    assert.equal(descendants(renderedRow.props.renderItem({entry}, {index: 0})).find(node => node.props.preferred !== undefined).props.preferred, false,
      'the start is the registry\'s one entry request, not a card asking for itself');
    rowsBox(tree).props.onLayout(layout(820));
    rowBox(tree).props.onLayout(layout(0));
    descendants(tree).find(node => node.type === 'Row').props.onItemFocus({entry});
    frames.shift()!(0); // Focus may see the old, expanded hero box.
    assert.equal(anchors.at(-1)?.[0], 820);
    tree = render();
    assert.ok(descendants(tree).some(node => node.type === 'FocusPreviewHero'), 'the hero follows the focused card');
    rowsBox(tree).props.onLayout(layout(588));
    assert.deepEqual(anchors.at(-1), [588, 300, 1080, 1080, 588, 96]);
    // A TV row (492) never fits between the 588 preview hero and a 96 bottom margin; kept merely visible, focus arriving
    // from below (the first landing) left Home 96 px higher than focus arriving from above.
    assert.equal(anchorOptions[2], 'start', 'the focused row\'s top sits under the preview hero whichever way focus came');
    const count = anchors.length;
    for (const props of [{railExpanded: true}, {active: false}]) rowsBox(render(props)).props.onLayout(layout(820));
    currentAuthority = {...authority, serverId: 'foreign'};
    rowsBox(render()).props.onLayout(layout(588));
    assert.equal(anchors.length, count, 'rail, hidden Home and stale authorities cannot move the focused page');
  } finally {globalThis.requestAnimationFrame = previousFrame;}
});
