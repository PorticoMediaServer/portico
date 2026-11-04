// @fabric-react
// Sol 8 on #823 (r2): Fabric commits a child's layout effects before its parent's, so the aimed row asks to be revealed
// before Settings has committed its aim. Real SettingsScreen and SettingsRowFlash on the production Fabric reconciler;
// only native hosts and unrelated page contents are stand-ins.
import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus.ts';
const reactNamespace = {...React, default: React};
const search = {tv:false,covered:false,matches:[{address:'playback',rowId:'subtitles'}],entries:[] as unknown[]};
const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree,...nodes(tree.children)] : [];
async function fixture() {
  let measures=0; const scrolls: unknown[]=[];
  const h = await fabricHost({publicInstance: (tag: unknown) => ({tag, getNativeScrollRef(){return this;}, scrollTo(v: unknown){scrolls.push(v);}, measureLayout(_host: unknown, ok: (...args: number[]) => void){measures++;ok(0,1000,300,40);}})});
  const motion={timing:()=>({})};
  const {SettingsRowFlash} = await screenModule(new URL('../src-next/kit/shell/SettingsRowTarget.tsx', import.meta.url), {
    react:reactNamespace, 'react-native-reanimated':{default:{View:'AnimatedView'},useSharedValue: (value: unknown)=>React.useRef({value}).current,useAnimatedStyle:(fn: () => unknown)=>fn(),withTiming:(v: unknown)=>v},
    '../useMotion':{useMotion:()=>motion,useReducedMotion:()=>false},'../theme':{useTheme:()=>({colors:{accent:'#409fd4'}})},'./visual':{alpha:()=> '#409fd4'}
  });
  const TargetProvider = ({value}: any) => value.flash ? React.createElement(SettingsRowFlash,{...value.flash,reveal: value.flash.reveal}) : null;
  const retirement=new AbortController(), role='member', initialWidth=800, hardwareBack: (() => boolean)[]=[], opens=[];
  let navigation={revision:1,route:{kind:'shell',destination:{kind:'settings'}}};
  const guarded=(v: unknown)=>v;
  const context={current:()=>true,account:{account:{role,username:'Viewer'},canManage:false,capabilities:{}},authority:()=>({}),navigation:{subscribe:()=>()=>{},getSnapshot:()=>navigation,isCurrent:()=>true,back(){},open(){}}};
  const page=(id: string)=>({id,title:id,icon:'settings',sections:[]});
  const structure={headings:[{id:'account',pages:['profile','playback','region'].map(page)}]};
  const theme={isTV:false,width:800,platform:'tablet',colors:new Proxy({},{get:(_t,key)=>String(key)})};
  const {NativeSettingsScreen} = await screenModule(new URL('../src-next/screens/settings-live/SettingsScreen.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true},  './tv-settings-focus': tvSettingsFocus,
    react: reactNamespace,
    'react-native': {AppState: {addEventListener: () => ({remove(){}})}, BackHandler: {addEventListener: (_name: string, listener: () => boolean) => {hardwareBack.push(listener); return {remove: () => {hardwareBack.splice(hardwareBack.indexOf(listener), 1);}};}}, Dimensions: {get: () => ({width: theme.width, height: 844})}, NativeModules: {}, Platform: {OS: 'ios'}, ScrollView: 'ScrollView', StatusBar: {}, Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../../../../../packages/client-core/src/presentation/settings-structure': {settingsStructure: () => structure, searchSettings: () => search.matches, findSettingsPage: (searchable: any, address: string) => {for (const heading of searchable.headings) {const found = heading.pages.find((value: any) => value.id === address); if (found) return {heading, page: found};} return undefined;}},
    '../../../../../packages/client-core/src/presentation/language-list': {}, '../../../../../packages/client-core/src/presentation/region-choices': {},
    '../../../../../packages/i18n/src': {defaultI18n: {number: String}}, '../../../../../packages/client-core/src/recommendation-feedback': {}, '../../../../../packages/design/src/next-icons': {nextIcons: {}},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => undefined, focus: () => true, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0}), useCovered: () => search.covered},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/shell': {SettingsRowTargetContext: {Provider: TargetProvider}, SettingsRow: 'SettingsRow', SettingsSheet: 'SettingsSheet', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'}, '../../kit/shell/visual': {shellType: () => ({}), alpha: (value: string) => value}, '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateView: 'StateView'}, '../../kit/states/sentence-parts': {}, '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})}, '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (activation: any, address: any) => {search.entries.push({activation, address});}},
    '../../shell/chrome': {setNativeScreenChromeHidden() {}}, './Screen': {Screen: 'Screen', useScreenBack() {}, useScreenEntry() {}}, './settings-actions': {},
    '../../../../../packages/contracts/src/routes': {}, '../../../../../packages/client-core/src/settings-configuration': {}, '../../../../../packages/client-core/src/presentation/settings-bindings': {}, '../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: [{id: 'signin', scope: 'server', title: 'signin', icon: 'settings'}]},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'Libraries'}, '../../kit/shell/ServerIdentity': {ServerBand: 'ServerBand', ServerHeader: 'ServerHeader'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'LiveOwner'}, './live-owner-controller': {NativeLiveOwnerController: class {}}, './recordings-controller': {createNativeRecordingsController: () => ({})}, '../../platform/server-connections': {nativeRouteCrypto: {}}, '../../../../../packages/client-core/src/administration': {parseUpdateReport: () => ({updateAvailable: false})},
    './server/TroubleshootingPage': {NativeServerTroubleshooting: 'Troubleshooting'}, './server/StoragePage': {NativeServerStorage: 'Storage'}, './server/IntegrationsPage': {NativeServerIntegrations: 'Integrations'}, './server/GeneralPage': {NativeServerGeneral: 'General'}, './server/RemoteAccessPage': {NativeRemoteAccessPage: 'RemoteAccess'}, './server/StreamingPage': {NativeServerStreaming: 'Streaming'}, './server/SegmentsPage': {NativeServerSegments: 'Segments'}, './server/PeoplePage': {NativeServerPeople: 'People'}, './server/SignInPage': {NativeServerSignIn: 'SignIn'}, './server/MetadataPage': {NativeServerMetadata: 'Metadata'}, './server/HistoryPage': {NativeServerHistory: 'History'},
    './server/PhoneServerPageHeader': {PhoneServerPageHeader: 'PhoneHeader'}, '../settings/remote-sources/NativeRemoteSourcesScreen': {NativeRemoteSourcesScreen: 'RemoteSources'}, './DashboardScreen': {NativeDashboardScreen: 'Dashboard'},
    '../../platform/playback/equalizer': {}, '../../tv/auth/TVHandoff': {},
    './use-tv-voice-search': {useTVVoiceSearch: () => ({})}, '../../platform/playback/passthrough-formats': {}, './NativeDynamicRangeRow': {}, '../../platform/playback/dynamic-range-matching': {}, './NativeApplePassthroughRow': {}, './NativeCapabilityRows': {}, './NativeFrameRateRow': {}, './NativePassthroughRow': {}, './NativeLossNoticesRow': {}, './NativeSafeModeRow': {}, './NativeServerOfflineNoticeRow': {NativeServerOfflineNoticeRow: 'NativeServerOfflineNoticeRow'},
  });
  const shell={profile:{name:'Profile'},account:{actions:[]}}; const state={};
  const runtime={retirementSignal:retirement.signal,text:{t:(id: string)=>id},subscribe:()=>()=>{},getSnapshot:()=>shell};
  const controller={subscribe:()=>()=>{},getSnapshot:()=>state,lifetime:{authority:{},current:()=>true}};
  const props={runtime,controller,ports:{context,settingsContext:()=>({}),serverName:()=> 'Server'},devicePreferences:{}};
  await h.render(React.createElement(NativeSettingsScreen, props));
  const open = async () => {
    nodes(h.root).find((n: any)=>n.props?.testID==='settings-search').props.onChangeText('sub'); await h.settle();
    nodes(h.root).find((n: any)=>n.type==='SettingsSearchResults').props.onNavigate('playback','subtitles'); await h.settle();
  };
  const flash = () => nodes(h.root).find((n: any)=>n.type==='AnimatedView');
  /** The page scroller's native layout (its viewport height). */
  const viewport = async (height: number) => {nodes(h.root).find((n: any)=>n.type==='ScrollView'&&n.props.onLayout).props.onLayout({nativeEvent:{layout:{x:0,y:0,width:390,height}}}); await h.settle();};
  return {open, flash, viewport, settle: () => h.settle(), unmount: () => h.unmount(), get measures() {return measures;}, scrolls};
}

test('the first aimed row is revealed once the page has committed its aim, and a later native layout measures nothing more', async () => {
  const f = await fixture();
  try {
    await f.viewport(400);
    await f.open(); assert.ok(f.flash());
    f.flash().props.onLayout(); await f.settle();
    assert.equal(f.measures, 1, 'the aimed row is measured once'); assert.equal(f.scrolls.length, 1, 'and scrolled to');
    f.flash().props.onLayout(); await f.settle();
    assert.equal(f.measures, 1); assert.equal(f.scrolls.length, 1);
  } finally {await f.unmount();}
});

test('a second opening of an already laid-out row is revealed at commit, with no new layout event', async () => {
  const f = await fixture();
  try {
    await f.viewport(400);
    await f.open(); f.flash().props.onLayout(); await f.settle();
    assert.equal(f.scrolls.length, 1);
    await f.open();
    assert.equal(f.measures, 2, 'the new opening measures its row once its aim is committed'); assert.equal(f.scrolls.length, 2);
  } finally {await f.unmount();}
});

// Sol 8 on #823 (r3): on a phone's first opening the page's scroller has just mounted and has no height yet, and Fabric's
// measureLayout answers at once. The reveal waits for the scroller's first real layout, then centres once.
test('a first opening waits for the page scroller\'s first measured height, then centres the row once', async () => {
  const f = await fixture();
  try {
    await f.open(); f.flash().props.onLayout(); await f.settle();
    assert.equal(f.scrolls.length, 0, 'no reveal against a viewport of height zero');
    await f.viewport(0);
    assert.equal(f.scrolls.length, 0, 'a zero-height layout is not a viewport yet');
    await f.viewport(400);
    assert.deepEqual(f.scrolls, [{y: 820, animated: false}], 'centred in the 400 pt viewport: 1000 - (400 - 40) / 2');
    await f.viewport(400); f.flash().props.onLayout(); await f.settle();
    assert.equal(f.measures, 1, 'decided once for the opening'); assert.equal(f.scrolls.length, 1);
  } finally {await f.unmount();}
});
