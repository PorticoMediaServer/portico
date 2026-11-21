// @fabric-react
// Sol 8 on #823 (r2): Fabric commits a child's layout effects before its parent's, so the aimed row asks to be revealed
// before Settings has committed its aim. Real SettingsScreen and SettingsRowFlash on the production Fabric reconciler;
// only native hosts and unrelated page contents are stand-ins.
import {settingsFixture} from './helpers/settings-fixture.mjs';
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
  let keyboardDismissals = 0;
  let navigation={revision:1,route:{kind:'shell',destination:{kind:'settings'}}};
  const guarded=(v: unknown)=>v;
  const context={current:()=>true,account:{account:{role,username:'Viewer'},canManage:false,capabilities:{}},authority:()=>({}),navigation:{subscribe:()=>()=>{},getSnapshot:()=>navigation,isCurrent:()=>true,back(){},open(){}}};
  const page=(id: string)=>({id,title:id,icon:'settings',sections:[]});
  const structure={headings:[{id:'account',pages:['profile','playback','region'].map(page)}]};
  const theme={isTV:false,width:800,platform:'tablet',colors:new Proxy({},{get:(_t,key)=>String(key)})};
  const {NativeSettingsScreen} = await settingsFixture({'../../../../../packages/client-core/src/home-selection/list-destinations': {withGuardedRaw: (api: unknown) => api}, '../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true},  './tv-settings-focus': tvSettingsFocus,
    react: reactNamespace,
    'react-native': {AppState: {addEventListener: () => ({remove(){}})}, BackHandler: {addEventListener: (_name: string, listener: () => boolean) => {hardwareBack.push(listener); return {remove: () => {hardwareBack.splice(hardwareBack.indexOf(listener), 1);}};}}, Dimensions: {get: () => ({width: theme.width, height: 844})}, Keyboard: {dismiss: () => {keyboardDismissals++;}}, NativeModules: {}, Platform: {OS: 'ios'}, ScrollView: 'ScrollView', StatusBar: {}, Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../../../../../packages/client-core/src/presentation/settings-structure': {settingsStructure: () => structure, searchSettings: () => search.matches, findSettingsPage: (searchable: any, address: string) => {for (const heading of searchable.headings) {const found = heading.pages.find((value: any) => value.id === address); if (found) return {heading, page: found};} return undefined;}},
    '../../../../../packages/client-core/src/presentation/language-list': {}, '../../../../../packages/client-core/src/presentation/region-choices': {},
    '../../../../../packages/i18n/src': {defaultI18n: {number: String}}, '../../../../../packages/client-core/src/recommendation-feedback': {}, './settings-icon': await import('../src-next/screens/settings-live/settings-icon.ts'), '../../../../../packages/design/src/next-icons': {nextIcons: {}},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => undefined, focus: () => true, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0}), useCovered: () => search.covered},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/shell': {SettingsRowTargetContext: {Provider: TargetProvider}, SettingsRow: 'SettingsRow', SettingsSheet: 'SettingsSheet', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles', SettingsSectionLabel: 'SettingsSectionLabel'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'}, '../../kit/shell/visual': {shellType: () => ({}), alpha: (value: string) => value}, '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateView: 'StateView'}, '../../kit/states/sentence-parts': {}, '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})}, '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (activation: any, address: any) => {search.entries.push({activation, address});}},
    '../../shell/chrome': {setNativeScreenChromeHidden() {}}, './Screen': {Screen: 'Screen', useScreenBack() {}, useScreenEntry() {}}, './settings-actions': {},
    '../../../../../packages/contracts/src/routes': {}, '../../../../../packages/client-core/src/settings-configuration': {}, '../../../../../packages/client-core/src/presentation/settings-bindings': {}, '../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: [{id: 'signin', scope: 'server', title: 'signin', icon: 'settings'}]},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'Libraries'}, '../../kit/shell/ServerIdentity': {ServerBand: 'ServerBand', ServerHeader: 'ServerHeader'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'LiveOwner'}, './live-owner-controller': {NativeLiveOwnerController: class {}}, './recordings-controller': {createNativeRecordingsController: () => ({})}, '../../platform/server-connections': {nativeRouteCrypto: {}},
    './server/TroubleshootingPage': {NativeServerTroubleshooting: 'Troubleshooting'}, './server/StoragePage': {NativeServerStorage: 'Storage'}, '../../touch/settings/ServerIntegrationsPage': {ServerIntegrationsPage: 'Integrations'}, './ServerGeneralPage': {ServerGeneralPage: 'General'}, './ServerRemoteAccessPage': {ServerRemoteAccessPage: 'RemoteAccess'}, '../../touch/settings/ServerStreamingPage': {ServerStreamingPage: 'Streaming'}, '../../touch/settings/ServerSegmentsPage': {ServerSegmentsPage: 'Segments'}, '../../touch/settings/ServerPeoplePage': {ServerPeoplePage: 'People'}, './server/SignInPage': {NativeServerSignIn: 'SignIn'}, './server/MetadataPage': {NativeServerMetadata: 'Metadata'}, '../../touch/settings/ServerHistoryPage': {ServerHistoryPage: 'History'},
