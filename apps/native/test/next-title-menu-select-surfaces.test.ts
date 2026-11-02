import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {PlaybackOptionsClient} from '../../../packages/client-core/src/playback-v1/options.ts';
import {localV1Http} from '../../../packages/client-core/src/playback-v1/local-http.ts';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {episodeLabel} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import {FileInfoSession, fileInfoUsesGroups} from '../../../packages/client-core/src/file-info-session.ts';
import {readNativePlaybackInfo} from '../src-next/screens/watch/playback-info.ts';
import * as cleanup from '../../../packages/client-core/src/cleanup.ts';
import {nativeWatchTracks} from '../src-next/screens/watch/track-identity.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';
import {nativeVersionChoices, nativeVersionNames} from '../src-next/screens/watch/version-options.ts';
import {NativeDiscTitlesController} from '../src-next/screens/watch/disc-titles-controller.ts';
import {prepareDiscTitle, readDiscTitles} from '../src-next/screens/watch/disc-titles.ts';
import {NativeWatchPresenters} from '../src-next/screens/watch/presenters.ts';
import {NativeScreenServices} from '../src-next/screens/screen-services.ts';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap.ts';
import type {NativeSelection} from '../src-next/screens/selection/native-selection.ts';

// W-18: DESIGN puts Select in every card's hold-OK menu. The shared builder draws the
// row (WatchOverlays) and each surface hands it the card's own selection entry; the
// show page's episode list is the surface that used to hand it nothing.
const {react} = hooks();
let detailProjection: any = {item: {id: 'show', kind: 'show', title: 'Show'}, scope: {serverId: 'server', libraryId: 'library', itemId: 'show', viewerFence: 'viewer'},
  revision: {catalog: 1, viewer: 1}, actions: [], related: {rows: [{id: 'more', heading: 'More like this', entries: [{id: 'related-film', kind: 'movie', title: 'Related', libraryId: 'library'}]}]}, personal: {}};
const detailStub = {DetailService: class {
  snapshot: any = {phase: 'idle', data: null}; listeners = new Set<() => void>();
  async select() {this.snapshot = {phase: detailProjection ? 'ready' : 'error', data: detailProjection};}
  getSnapshot = () => this.snapshot; subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  mutate = async () => {}; dispose = () => {this.snapshot = {phase: 'idle', data: null};}}};
const episodeAtoms = {react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 800, height: 600})},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/Icon': {Icon: 'Icon'},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', TextField: 'TextField'},
  '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true},
  '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/theme': {useTheme: () => ({colors: {}, type: {}})}};
const seasons = await screenModule(new URL('../src-next/screens/watch/SeasonsSheet.tsx', import.meta.url), episodeAtoms);
const episodes = await screenModule(new URL('../src-next/screens/watch/EpisodePanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, ...episodeAtoms,
  '../../../../../packages/client-core/src/presentation/kit-model': {episodeLabel},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText}, './SeasonsSheet': seasons,
  '../../kit/cards': {EpisodeTile: 'EpisodeTile'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'}});
const {createNativeWatchScreens} = await screenModule(new URL('../src-next/screens/watch/factory.tsx', import.meta.url), {'../share/share-actions': {shareLinkActions: () => []},
  react, '../../../../../packages/client-core/src/cleanup': cleanup, '../../../../../packages/client-core/src/playback-v1/options': {PlaybackOptionsClient},
  '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http},
  '../../../../../packages/client-core/src/detail': detailStub,
  '../../../../../packages/client-core/src/continue-watching': {setContinueDismissed: async () => {}},
  '../../platform/layout/full-window': {nativeFullWindowOrientation: undefined},
  './disc-titles-controller': {NativeDiscTitlesController}, './disc-titles': {prepareDiscTitle, readDiscTitles},
  '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../shell/model': {sameAuthority}, './controls': {NativeWatchControls}, '../../../../../packages/client-core/src/file-info-session': {FileInfoSession, fileInfoUsesGroups}, './FileInfoPanel': {NativeFileInfoPanel: 'FileInfoPanel'}, './playback-info': {readNativePlaybackInfo},
  './track-identity': {nativeWatchTracks}, './EpisodePanel': episodes,
  './title-controller': {NativeTitleController: class NativeTitleControllerStub {
    entry = show;
    snapshot: any = {entry: show, episodes: [episode, related], detail: {data: detailProjection}, related: {}, retired: false, place: {}, workspace: {phase: 'idle', data: null}};
    getSnapshot = () => this.snapshot;
    subscribe = () => () => {};
    dispose = () => {};
  }}, './title-starts': {NativeTitleStarts: class {invalidate() {} dispose() {}}},
  './TitleScreen': {NativeTitleScreen: 'Title'},
  './version-destination': {NativeVersionChooserController}, './version-options': {nativeVersionChoices, nativeVersionNames},
  './WatchHost': {NativeWatchHost: 'WatchHost'}, './WatchChrome': {NativeWatchChrome: 'Chrome'}, './picture-slot': {NativeWatchPicture: 'Picture'},
});

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const show = {id: 'show', kind: 'show', title: 'Actual show', libraryId: 'library'};
const episode = {id: 'episode-1', kind: 'episode', title: 'Episode 1', libraryId: 'library'};
const related = {id: 'related-film', kind: 'movie', title: 'Related film', libraryId: 'library'};
const card = (entry: {id: string}) => ({active: true, selected: false, onToggle() {}, onEnter() {entered.push(entry.id);}});
const entered: string[] = [];
const settle = async () => {for (let turn = 0; turn < 12; turn++) await new Promise(resolve => setImmediate(resolve));};

/** The page's own selection owner: the factory creates one and the cards ask it. */
function selectionOwner(): NativeSelection {
  return {subscribe: () => () => {}, getSnapshot: () => 0, start() {}, clear: () => true, register: () => () => {},
    item: (entry: any) => entry.id === 'related-film' || entry.id === 'episode-1' ? card(entry) : undefined,
    render: () => null, dispose() {}} as unknown as NativeSelection;
}

function fixture() {
  const menus: any[] = [], listeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  const api = new HttpLocalApi('https://portico.test', '', async () => new Response(JSON.stringify({}), {status: 404}));
  const source = {authority, retired: false, fence: undefined as any, dock: {local: {expanded: false, fence: undefined as any}}, playback: {phase: 'ready'}, session: undefined};
  const navigation = {getSnapshot: () => ({authority, revision: 3, route: {kind: 'title', entry: show}}), subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}, back() {}, open() {return true;}};
  const playback = {getSnapshot: () => source, subscribe: () => () => {}, start: async () => {}, cataloguePlayback: () => undefined} as any;
  const common = new NativeScreenServices({navigation, current: () => true, authority: () => authority, subscribeAuthority: () => () => {}} as any, async () => 'request');
  const presenters = new NativeWatchPresenters({api, navigation, playback, current: () => true, authority: () => authority,
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => authorityListeners.delete(listener);}} as unknown as NativeShellBootstrapContext, common);
  const selections: NativeSelection[] = [];
  const screens = createNativeWatchScreens({api, scope: {serverId: 'server', viewerId: 'viewer'}, authority, current: () => authority,
    subscribeCurrent: (listener: () => void) => {authorityListeners.add(listener); return () => authorityListeners.delete(listener);}, navigation, playback,
    playlistPicker: {open() {}, openAlbum() {}, render: () => null, dispose() {}}, watch: {register() {throw new Error('not mounted');}}, requestId: async () => 'request', isTV: true,
    startVersion: async () => {}, manageFile() {}, openFileInfo() {}, openMenu: (...args: unknown[]) => {menus.push(args);},
    title: {aboutLabel: 'About', navigate() {}, renderError() {}, reportError() {}, createSelection: () => {const owner = selectionOwner(); selections.push(owner); return owner;}},
  } as any);
  return {screens, menus, selections, presenters};
}

const renderTitle = (screens: any) => screens.title.render({kind: 'title', entry: show}, {} as any);

test('the show page\'s shared menu offers Select for its episode and related cards, using the card\'s own entry', async () => {
  const f = fixture();
  try {
    const element = renderTitle(f.screens);
    assert.equal(element.type, 'Title');
    assert.equal(typeof element.props.selection, 'function', 'the title page draws its cards with their selection entry');
    const card = element.props.selection(episode);
    assert.equal(typeof card?.onEnter, 'function');
    element.props.more(episode, {current: null});
    element.props.more(related, {current: null});
    await settle();
    for (const [capture, actions] of f.menus as any[]) {
      assert.equal(typeof actions.select, 'function', `${capture.entry.id} offers Select (DESIGN: every card's hold-OK menu)`);
    }
    const before = entered.length;
    (f.menus[0][1] as any).select();
    assert.deepEqual(entered.slice(before), ['episode-1'], 'Select starts this card\'s selection');
  } finally {f.screens.dispose(); f.presenters.dispose();}
});

test('a surface that hands the shared builder its own card entry still gets Select, as Home, library grids, search and album tracks do', async () => {
  const f = fixture();
  try {
    let started = 0;
    await f.screens.menu(episode, () => true, undefined, () => {started++;});
    assert.equal(typeof (f.menus[0][1] as any).select, 'function');
    (f.menus[0][1] as any).select();
    assert.equal(started, 1);
  } finally {f.screens.dispose(); f.presenters.dispose();}
});

test('a surface that can neither select nor hand an entry draws no Select row (never a dead one)', async () => {
  const f = fixture();
  try {
    await f.screens.menu({...show}, () => true);
    assert.equal((f.menus[0][1] as any).select, undefined, 'the show hero is not one of the page\'s cards');
  } finally {f.screens.dispose(); f.presenters.dispose();}
});
