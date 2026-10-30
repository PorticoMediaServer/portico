import {userMutationRoute} from '../../../../packages/client-core/src/user-mutation-routes.ts';
import {userMutationBridge} from './user-mutation-bridge.mjs';
import {readFileSync} from 'node:fs';
import {createRequire, registerHooks, stripTypeScriptTypes} from 'node:module';
import {compileFunction} from 'node:vm';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {transformSync} from 'rolldown/utils';
import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  randomUUID,
  sign,
  verify,
  createPublicKey,
} from 'node:crypto';

const require = createRequire(import.meta.url);
const React = require(join(
  dirname(require.resolve('react')),
  'cjs/react.production.js',
));
// Native accessibility labels inherit rendered descendant text when none is explicit.
const nativeText = node =>
  Array.isArray(node)
    ? node.map(nativeText).join('')
    : typeof node === 'string'
    ? node
    : typeof node === 'function'
    ? nativeText(node({pressed: false}))
    : node?.props
    ? nativeText(node.props.children)
    : '';
const nativeLabel = host =>
  host.props.accessibilityLabel ?? nativeText(host.children);
const data = source => `data:text/javascript,${encodeURIComponent(source)}`;
const globalKey = '__porticoStandardHomeNativeBoundary';
const before = {
  dev: globalThis.__DEV__,
  frame: globalThis.requestAnimationFrame,
  cancelFrame: globalThis.cancelAnimationFrame,
  timeout: globalThis.setTimeout,
  clearTimeout: globalThis.clearTimeout,
  interval: globalThis.setInterval,
  clearInterval: globalThis.clearInterval,
  warn: console.warn,
  error: console.error,
};
const timeouts = new Set(),
  intervals = new Set();
// Delegate real clocks during the test. Keep handles only so final test disposal
// also clears app-global delayed connection retirement, without waiting 16s.
function installNativeClocks() {
globalThis.setTimeout = (callback, delay, ...args) => {
  const handle = before.timeout(() => {
    timeouts.delete(handle);
    callback(...args);
  }, delay);
  timeouts.add(handle);
  return handle;
};
globalThis.clearTimeout = handle => {
  timeouts.delete(handle);
  before.clearTimeout(handle);
};
globalThis.setInterval = (callback, delay, ...args) => {
  const handle = before.interval(callback, delay, ...args);
  intervals.add(handle);
  return handle;
};
globalThis.clearInterval = handle => {
  intervals.delete(handle);
  before.clearInterval(handle);
};
}
installNativeClocks();
const state = {
  React,
  hosts: new Map(),
  nextHost: 1,
  registrations: new Map(),
  events: new Map(),
  appState: new Set(),
  tvMenuEnabled: false,
  back: new Set(),
  modules: {},
  frames: new Map(),
};
globalThis[globalKey] = state;
globalThis.__DEV__ = false;
let frameId = 0;
const nativeFrame = callback => {
  const id = ++frameId;
  const timer = setImmediate(() => {
    state.frames.delete(id);
    callback(performance.now());
  });
  state.frames.set(id, timer);
  return id;
};
const nativeCancelFrame = id => {
  clearImmediate(state.frames.get(id));
  state.frames.delete(id);
};
globalThis.requestAnimationFrame = nativeFrame;
globalThis.cancelAnimationFrame = nativeCancelFrame;
const reactURL = data(
  `const R=globalThis.${globalKey}.React;export default R;export const {${Object.keys(
    React,
  )
    .filter(key => /^[A-Za-z_$][\w$]*$/.test(key))
    .join(',')}}=R;`,
);
const nativeURL = data(`import React from ${JSON.stringify(reactURL)};
 const S=globalThis.${globalKey};
 const flat=s=>Array.isArray(s)?Object.assign({},...s.map(flat)):s??{};
 function host(name){return function NativeBoundary({ref,children,...props}){
   const own=React.useRef(); if(!own.current)own.current={tag:S.nextHost++,contentOffset:{x:0,y:0},requestTVFocus(){S.hosts.get(this.tag)?.props.onFocus?.({nativeEvent:{}});},setDestinations(){},scrollTo(first=0,second=0){const offset=typeof first==='object'?first:{y:first,x:second};this.contentOffset={x:offset.x??0,y:offset.y??0};},
    // The installed list reads its command from the props of the host it currently
    // renders, not the props captured when this ref first initialised. VirtualizedList
    // counts getItemCount(data) or data.length, asks getItemLayout(data, index) for the
    // cell, and scrollToOffset moves the real scroll view along the list's own axis:
    // x when horizontal, y otherwise.
    scrollToIndex({index=0,viewOffset=0,viewPosition=0}={}){const latest=S.hosts.get(this.tag)?.props??{},data=latest.data,count=typeof latest.getItemCount==='function'?latest.getItemCount(data):data?.length??0;if(!(index>=0&&index<count))throw Error('scrollToIndex out of range: requested index '+index+' is out of 0 to '+(count-1));const layout=typeof latest.getItemLayout==='function'?latest.getItemLayout(data,index):undefined;if(!layout||!Number.isFinite(layout.offset)){if(typeof latest.onScrollToIndexFailed==='function')return latest.onScrollToIndexFailed({index,averageItemLength:0,highestMeasuredFrameIndex:0});throw Error('scrollToIndex requires getItemLayout or onScrollToIndexFailed in this fixture');}
     // The caller-declared style extent on the scroll axis is the drawn viewport; this boundary lays
     // out no measured cells, so viewPosition uses that value and viewOffset is exact.
     const style=flat(typeof latest.style==='function'?latest.style({pressed:false}):latest.style),extent=latest.horizontal?style.width:style.height,viewport=Number.isFinite(extent)?extent:0;
     const offset=Math.max(0,layout.offset-viewPosition*(viewport-layout.length))-viewOffset;
     this.contentOffset={...this.contentOffset,[latest.horizontal?'x':'y']:offset};this.scrolledToIndex=index;},
    measure(cb){cb(0,0,390,220,0,588)},measureInWindow(cb){cb(0,588,390,220)},setNativeProps(){}};
   // The scroll view's live native offset, not a prop the app set: native answers
   // with where the app's own scrollTo actually left the surface. Delivering the
   // resulting scroll event stays the test's controlled input.
   const value=own.current; React.useLayoutEffect(()=>{if(name==='ScrollView'&&!('contentOffset' in props))Object.defineProperty(props,'contentOffset',{configurable:true,get:()=>value.contentOffset});S.hosts.set(value.tag,{name,props,children,host:value});});
   React.useLayoutEffect(()=>{const release=typeof ref==='function'?ref(value):undefined;if(ref&&typeof ref!=='function')ref.current=value;return()=>{if(typeof release==='function')release();else if(typeof ref==='function')ref(null);else if(ref)ref.current=null;S.hosts.delete(value.tag);};},[ref,value]);
   const delivered=React.useRef(false);React.useLayoutEffect(()=>{if(delivered.current||!props.onLayout)return;delivered.current=true;const style=flat(typeof props.style==='function'?props.style({pressed:false}):props.style);props.onLayout({nativeEvent:{layout:{x:0,y:588,width:typeof style.width==='number'?style.width:390,height:typeof style.height==='number'?style.height:220}}});},[props.onLayout,props.style]);
   // FlatList draws its data through renderItem; this boundary draws every row rather
   // than a measured window, so the rows the caller supplied are the rows it renders.
   const items=name==='FlatList'&&typeof props.renderItem==='function'&&Array.isArray(props.data)
     ?props.data.map((item,index)=>React.createElement(React.Fragment,{key:typeof props.keyExtractor==='function'?props.keyExtractor(item,index):index},props.renderItem({item,index,separators:{highlight(){},unhighlight(){},updateProps(){}}})))
     :null;
   return React.createElement(React.Fragment,null,typeof children==='function'?children({pressed:false}):children,items);
 };}
 export const SafeAreaView=host('SafeAreaView'),View=host('View'),Text=host('Text'),Pressable=host('Pressable'),Image=host('Image'),TextInput=host('TextInput'),ScrollView=host('ScrollView'),KeyboardAvoidingView=host('KeyboardAvoidingView'),RefreshControl=host('RefreshControl'),TVFocusGuideView=host('TVFocusGuideView'),ActivityIndicator=host('ActivityIndicator'),Modal=host('Modal'),Switch=host('Switch'),FlatList=host('FlatList'),VirtualizedList=host('VirtualizedList');
 export const Share={share(){throw Error('Unexpected native Share operation in Home fixture')}};
 export const Platform={OS:'android',isTV:false,isPad:false,Version:36,constants:{Manufacturer:'test'}};
 export const TVEventControl={enableTVMenuKey:()=>{S.tvMenuEnabled=true;},disableTVMenuKey:()=>{S.tvMenuEnabled=false;}};
 export const NativeModules=S.modules,NativeEventEmitter=class{addListener(name,fn){const set=S.events.get(name)??new Set();S.events.set(name,set);set.add(fn);return{remove:()=>set.delete(fn)}}};
 export const DeviceEventEmitter=new NativeEventEmitter(),AppState={currentState:'active',addEventListener:(_,fn)=>{S.appState.add(fn);return{remove:()=>S.appState.delete(fn)}}};
 // This boundary has no attached screen reader, so the query answers as React Native does when the OS reports none; announcements and change subscriptions stay inert fixture controls.
 export const BackHandler={addEventListener:(_,listener)=>{S.back.add(listener);return{remove:()=>S.back.delete(listener)}}},AccessibilityInfo={isScreenReaderEnabled:async()=>false,isReduceMotionEnabled:async()=>true,addEventListener:()=>({remove(){}}),announceForAccessibility(){}};
 // Forward fixture-supplied responder events to the actual product callbacks;
 // this boundary does not simulate native gesture negotiation or movement.
 export const PanResponder={create:config=>({panHandlers:Object.fromEntries(Object.entries(config).filter(([key])=>key.startsWith('onPanResponder')||key.includes('ShouldSetPanResponder')).map(([key,callback])=>[key.replace('ShouldSetPanResponder','ShouldSetResponder').replace('onPanResponder','onResponder'),callback]))})};
 export const StyleSheet={create:s=>s,flatten:flat,absoluteFill:{position:'absolute',inset:0},absoluteFillObject:{position:'absolute',inset:0},hairlineWidth:1};
 export const Dimensions={get:()=>({width:390,height:844,scale:1,fontScale:1}),addEventListener:()=>({remove(){}})},PixelRatio={get:()=>1,getFontScale:()=>1},useWindowDimensions=()=>({width:390,height:844,scale:1,fontScale:1});
 export const TVEventHandler={addListener:()=>({remove(){}})},registerCallableModule=()=>{},AppRegistry={registerComponent:(name,factory)=>S.registrations.set(name,factory)},StatusBar=host('StatusBar'),UIManager={hasViewManagerConfig:()=>true},findNodeHandle=node=>node?.tag??null,requireNativeComponent=name=>host(name),useTVEventHandler=()=>{},I18nManager={isRTL:false};
 export const Alert={alert(){}},Keyboard={dismiss(){},isVisible:()=>false,addListener:()=>({remove(){}})},Linking={openURL:async()=>{},canOpenURL:async()=>true,addEventListener:()=>({remove(){}}),getInitialURL:async()=>null},useColorScheme=()=> 'dark';
 class Value{constructor(value){this.value=value}setValue(value){this.value=value}stopAnimation(){}interpolate(config){return {value:this.value,config}}};
 export const Easing={bezier:()=>x=>x,linear:x=>x};
 export const Animated={View:host('AnimatedView'),Value,timing:(value,config)=>({start:done=>{value.value=config.toValue;done?.({finished:true})},stop(){}}),parallel:()=>({start(){},stop(){}}),sequence:()=>({start(){},stop(){}}),loop:()=>({start(){},stop(){}})};
`);
const animatedURL = data(`import React from ${JSON.stringify(
  reactURL,
)};import {View,ScrollView} from ${JSON.stringify(nativeURL)};
 export const useSharedValue=value=>React.useRef({value}).current,useAnimatedStyle=fn=>fn(),useAnimatedRef=()=>React.useRef(null),useAnimatedScrollHandler=fn=>fn,useDerivedValue=fn=>({value:fn()}),cancelAnimation=()=>{},scrollTo=(ref,x=0,y=0)=>{ref?.current?.scrollTo?.({x,y});},runOnJS=fn=>fn,runOnUI=fn=>fn;
 export const useAnimatedReaction=(prepare,reaction)=>{const prior=React.useRef(null);React.useEffect(()=>{const next=prepare();reaction(next,prior.current);prior.current=next;});};
 export const withDecay=()=>{throw Error('Unexpected Guide decay in Home fixture')};
 export const withTiming=(value,config,done)=>{if(done)setImmediate(()=>done(true));return value},withSpring=withTiming,withDelay=(_,value)=>value;
 export const Easing={bezier:()=>x=>x,linear:x=>x},ReduceMotion={Never:'never'};export default{View,ScrollView,createAnimatedComponent:x=>x};`);
const svgNames = [
  'SvgXml',
  'Svg',
  'Defs',
  'Pattern',
  'LinearGradient',
  'RadialGradient',
  'Rect',
  'Stop',
  'Mask',
  'Path',
  'G',
  'ClipPath',
  'Circle',
  'Ellipse',
  'Line',
  'Polygon',
  'Polyline',
  'Use',
  'Image',
  'Text',
];
const svgURL = data(
  `import {View} from ${JSON.stringify(
    nativeURL,
  )};export default View;export const {${svgNames.join(
    ',',
  )}}=Object.fromEntries(${JSON.stringify(svgNames)}.map(k=>[k,View]));`,
);
const gestureURL = data(
  `import {View} from ${JSON.stringify(
    nativeURL,
  )};const pan=()=>new Proxy({},{get:(_,key)=>key==='then'?undefined:()=>pan()});export const Gesture={Pan:pan,Native:pan,Race:pan,Simultaneous:pan,Tap:pan},GestureDetector=View,GestureHandlerRootView=View;`,
);
const loader = registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'react') return {url: reactURL, shortCircuit: true};
    if (specifier === 'react-native')
      return {url: nativeURL, shortCircuit: true};
    if (specifier === 'react-native-webview')
      return {url: data(`export {View as default} from ${JSON.stringify(nativeURL)};`), shortCircuit: true};
    if (specifier === 'react-native-reanimated')
      return {url: animatedURL, shortCircuit: true};
    if (specifier === 'react-native-svg')
      return {url: svgURL, shortCircuit: true};
    if (specifier === 'react-native-gesture-handler')
      return {url: gestureURL, shortCircuit: true};
    // P2-73: the touch shell's native stack hosts each page in a plain view here.
    if (specifier === 'react-native-screens')
      return {
        url: data(
          `export {View as ScreenStack,View as ScreenStackItem,View as Screen,View as ScreenContainer} from ${JSON.stringify(nativeURL)};`,
        ),
        shortCircuit: true,
      };
    if (specifier === '@react-native-community/blur')
      return {
        url: data(
          `export {View as BlurView} from ${JSON.stringify(nativeURL)};`,
        ),
        shortCircuit: true,
      };
    if (
      specifier === '@react-native-masked-view/masked-view' ||
      specifier === 'react-native-qrcode-svg'
    )
      return {
        url: data(
          `export {View as default} from ${JSON.stringify(nativeURL)};`,
        ),
        shortCircuit: true,
      };
    if (specifier === 'react-native-safe-area-context')
      return {
        url: data(
          `export {View as SafeAreaProvider,View as SafeAreaView} from ${JSON.stringify(
            nativeURL,
          )};export const useSafeAreaInsets=()=>({left:0,right:0,top:44,bottom:34});`,
        ),
        shortCircuit: true,
      };
    if (
      specifier === '@bacons/text-decoder/install' ||
      specifier === 'react-native-url-polyfill/auto'
    )
      return {url: data('export {};'), shortCircuit: true};
    try {
      return next(specifier, context);
    } catch (error) {
      if (
        !['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT', 'MODULE_NOT_FOUND'].includes(
          error.code,
        ) ||
        !specifier.startsWith('.')
      )
        throw error;
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx', '.js']) {
        try {
          return next(specifier + suffix, context);
        } catch (retry) {
          if (
            !['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT', 'MODULE_NOT_FOUND'].includes(
              retry.code,
            )
          )
            throw retry;
        }
      }
      throw error;
    }
  },
  load(url, context, next) {
    if (url.endsWith('/app.json'))
      return {
        format: 'module',
        source: `const app=${readFileSync(
          fileURLToPath(url),
          'utf8',
        )};export default app;export const {name,displayName}=app;`,
        shortCircuit: true,
      };
    if (/\.(png|ttf)$/.test(url))
      return {
        format: 'commonjs',
        source: 'module.exports=1;',
        shortCircuit: true,
      };
    if (url.endsWith('.ts')) {
      const source = readFileSync(fileURLToPath(url), 'utf8');
      try {stripTypeScriptTypes(source);}
      catch (error) {
        if (error?.code !== 'ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX') throw error;
        // Synchronous require() bypasses the async test loader. Preserve the
        // same parameter-property support at this real-App module boundary.
        return {format: 'module', source: stripTypeScriptTypes(source, {mode: 'transform', sourceUrl: url}), shortCircuit: true};
      }
    }
    if (!url.endsWith('.tsx')) return next(url, context);
    const file = fileURLToPath(url),
      out = transformSync(file, readFileSync(file, 'utf8'), {
        lang: 'tsx',
        jsx: {runtime: 'classic'},
        target: 'es2022',
      });
    if (out.errors.length) throw Error(JSON.stringify(out.errors));
    return {
      format: 'module',
      source:
        (/\brequire\(/.test(out.code)
          ? "import{createRequire as __testRequire}from'node:module';const require=__testRequire(import.meta.url);\n"
          : '') + out.code,
      shortCircuit: true,
    };
  },
});

/** Import the actual product entry, then render its registered App with real
 * production React/Fabric. Only native hosts/modules, animation delivery and
 * native transport responses are controlled. Bootstrap/core/controller modules
 * resolve from source unchanged; no runtime prop or business facade is supplied.
 * Native secure records contain synthetic test credentials, never device state.
 */
/** @param {{setup?: boolean, mountUI?: boolean, accountRole?: 'owner' | 'member', deleteCapability?: boolean, privacyFence?: string, nativeResponse?: (request: any, fixture: any) => Promise<any>, setupResponse?: (request: any, fixture: any) => Promise<any>}} options */
export async function standardHomeApp({
  setup = false,
  mountUI = true,
  accountRole = 'member',
  deleteCapability = false,
  privacyFence,
  nativeResponse,
  setupResponse,
} = {}) {
  installNativeClocks();
  globalThis[globalKey] = state;
  globalThis.__DEV__ = false;
  globalThis.requestAnimationFrame = nativeFrame;
  globalThis.cancelAnimationFrame = nativeCancelFrame;
  const {FakePlaybackServer} = await import(
    '../../../../packages/client-core/src/playback-v1/testing/fake-server.ts'
  );
  const {routePath} = await import(
    '../../../../packages/contracts/src/routes.ts'
  );
  const {selectedViewerRecord} = await import(
    '../../../../packages/client-core/src/viewer-selection.ts'
  );
  const {setInstallationIdentity} = await import(
    '../../../../packages/client-core/src/installation.ts'
  );
  setInstallationIdentity(undefined);
  const pair = generateKeyPairSync('ed25519'),
    publicKeyBytes = pair.publicKey
      .export({format: 'der', type: 'spki'})
      .subarray(-32),
    b64 = value => Buffer.from(value).toString('base64url'),
    digest = value => createHash('sha256').update(value).digest();
  const pin = {
    serverId:
      'srv_' +
      b64(
        digest(
          Buffer.concat([
            Buffer.from('portico.server.identity.v1\0'),
            publicKeyBytes,
          ]),
        ),
      ),
    publicKey: b64(publicKeyBytes),
    fingerprint: b64(digest(publicKeyBytes)),
  };
  const origin = 'https://standard-home.test',
    installationId = 'standard_home_native_0123456789abcdef0123456789',
    viewer = {
      serverId: pin.serverId,
      accountId: 'account',
      profileId: 'profile',
      authority: 'local',
      role: accountRole,
    };
  const session = {
    accessToken: 'fixture_access_only',
    refreshToken: 'fixture_refresh_only',
    installationId,
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
    sessionFamilyId: 'fixture_family',
    tokenGeneration: '1',
    authorizationHorizon: new Date(Date.now() + 86400000).toISOString(),
    viewer,
  };
  const store = new Map(
    setup
      ? []
      : [
          [
            'accounts.sessions.v1',
            JSON.stringify({version: 1, revision: 1, selectedServerId: pin.serverId, servers: [{signIn: 'local', connection: {
              version: '1',
              logicalOrigin: origin,
              session,
              pin,
              routes: {paired: [origin]},
            }}]}),
          ],
        ],
  );
  if (!setup) store.set('accounts.server-names.v1', JSON.stringify({version: 1, servers: [{serverId: pin.serverId, fingerprint: pin.fingerprint, name: 'Synthetic living room'}]}));
  const requests = [],
    receipts = [],
    commands = [],
    cancellations = [],
    nativeErrors = [],
    pending = new Map();
  let selected = setup
      ? null
      : JSON.stringify(selectedViewerRecord(origin, session, true)),
    lease = 0,
    homeVersion = 1,
    homeFence = 'home-fence',
    /** @type {string | undefined} H-11: absent like an older server unless a test sets it. */
    homePrivacy = privacyFence;
  const backend = new FakePlaybackServer({
    viewer,
    deviceId: installationId,
    reportEveryMs: 1000,
  });
  const homeEntry = () => ({
    id: 'movie',
    libraryId: 'movies',
    kind: 'movie',
    title:
      homeVersion === 1
        ? 'Native boundary test movie'
        : `Native boundary test movie v${homeVersion}`,
    available: true,
    durationMs: 180000,
    progressMs: 12000,
    navigation: {view: 'item', entityId: 'movie'},
    playback: {itemId: 'movie', startMs: 12000},
    homeActions: {
      play: true,
      queue: true,
      collection: true,
      myList: true,
      details: true,
      delete: accountRole === 'owner' && deleteCapability,
      state: {
        watchlisted: false,
        favorite: false,
        watched: false,
        rating: null,
        personalRevision: 1,
        containerRevision: 1,
      },
      selection: {
        play: true,
        queue: true,
        collection: true,
        myList: true,
        delete: deleteCapability,
      },
    },
  });
  const homeRow = () => ({
    id: 'continue',
    viewerFence: homeFence,
    title: 'Continue Watching',
    kind: 'continue',
    artworkShape: 'landscape',
    entries: [homeEntry()],
    privacySensitivity: 'catalog',
    policyState: 'available',
    priority: 1,
    required: true,
    hideable: false,
    reorderable: false,
    critical: false,
    cursorCapable: false,
    total: 1,
    start: 0,
    limit: 20,
    hasMore: false,
    nextCursor: '',
    endpoint: '',
    revision: {catalog: homeVersion, viewer: 1},
  });
  const homeDoc = () => ({
    serverId: pin.serverId,
    viewerFence: homeFence,
    ...(homePrivacy !== undefined ? {privacyFence: homePrivacy} : {}),
    rows: [homeRow()],
    spotlight: [],
    layout: {revision: 1, rowOrder: ['continue'], hiddenRowIds: []},
    revision: {catalog: homeVersion, viewer: 1},
    generatedAt: new Date().toISOString(),
  });
  const effective = {
    'playback.defaultSpeed': 1,
    'audiobooks.defaultSpeed': 1,
    'playback.skipBackMs': 10000,
    'playback.skipForwardMs': 30000,
    'playback.introSkip': 'ask',
    'playback.recapSkip': 'ask',
    'playback.creditsSkip': 'off',
    'playback.subtitleSize': 'medium',
    'playback.subtitleBackground': 'translucent',
    'playback.subtitleColor': 'white',
    'playback.subtitlePosition': 'bottom',
    'playback.subtitleFont': 'default',
    'playback.keepSubtitleStyling': true,
    'playback.useSystemCaptionStyle': false,
    'search.rememberHistory': false,
  };
  const prefs = () => ({
    deviceClass: 'mobile',
    registryRevision: 'fixture-preferences',
    registry: {
      revision: 'fixture-preferences',
      fields: Object.entries(effective).map(([key, value]) => ({
        key,
        type: typeof value === 'number' ? 'number' : typeof value === 'boolean' ? 'boolean' : 'string',
        default: key === 'search.rememberHistory' ? true : value,
        group: key.split('.')[0],
        labelKey: 'preferences.' + key,
        scopes: key === 'search.rememberHistory' ? ['profile-server'] : ['profile-server', 'profile-device-class'],
      })),
    },
    documents: ['profile-server', 'profile-device-class'].map(scope => ({
      scope,
      revision: 1,
      activeRevision: 1,
      digest: 'd'.repeat(64),
      values: scope === 'profile-server' ? effective : {},
    })),
    effective,
    effectiveSource: Object.fromEntries(
      Object.keys(effective).map(key => [key, 'profile-server']),
    ),
    clampedFields: [],
  });
  const envelope = (domain, value) => ({
    scope: {
      serverId: pin.serverId,
      viewerFence: (domain === 'preferences' ? 'a' : 'b').repeat(64),
    },
    data: value,
  });
  let fault;
  const reply = async request => {
    const url = new URL(origin + request.path),
      path = url.pathname;
    if (path === routePath('prove_network_identity')) {
      const value = {
        kind: 'portico.route-proof',
        version: '1',
        baseUrl: origin,
        nonce: request.body.nonce,
        ...pin,
        issuedAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 15000).toISOString(),
      };
      const payload = b64(Buffer.from(JSON.stringify(value)));
      return {
        status: 200,
        headers: {},
        body: {
          payload,
          signature: b64(
            sign(null, Buffer.from(payload, 'base64url'), pair.privateKey),
          ),
        },
      };
    }
    if (nativeResponse) {
      const response = await nativeResponse(request, {
        pin,
        viewer,
        session: {...session, serverIdentity: {publicKey: pin.publicKey, fingerprint: pin.fingerprint}},
        installationId,
        origin,
      });
      if (response) return response;
    }
    if (setup && setupResponse) {
      const response = await setupResponse(request, {
        pin,
        viewer,
        session: {
          ...session,
          serverIdentity: {
            publicKey: pin.publicKey,
            fingerprint: pin.fingerprint,
          },
        },
        installationId,
        origin,
      });
      if (response) return response;
    }
    if (path === routePath('get_user_mutation_policy')) return {status: 200, body: {serverTimeMs: Date.now(), retiredBeforeMs: 0}};
    if (path.startsWith('/v1/user-mutations/')) {
      const receipt = mutationReceipts.get(decodeURIComponent(path.slice('/v1/user-mutations/'.length)));
      return receipt ? {status: 200, body: receipt} : {status: 404, body: {error: {code: 'receipt_unknown'}}};
    }
    if (path === routePath('get_system')) return {status: 200, body: {id: pin.serverId, name: 'Synthetic living room', version: '1', move: null, signIn: 'local', setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}};
    if (path === routePath('get_me')) return {status: 200, body: {viewer: {...viewer, role: accountRole}}};
    if (path === routePath('get_home')) return {status: 200, body: homeDoc()};
    if (path === routePath('get_preferences'))
      return {status: 200, body: envelope('preferences', prefs())};
    if (path === routePath('get_libraries'))
      return {
        status: 200,
        body: {items: [{id: 'movies', name: 'Movies', kind: 'movies'}]},
      };
    if (path === routePath('get_direct_account'))
      return {
        status: 200,
        body: {
          authority: 'local',
          serverId: pin.serverId,
          canManage: accountRole === 'owner',
          capabilities: {
            manageAccount: accountRole === 'owner',
            managePeople: accountRole === 'owner',
            manageServer: accountRole === 'owner',
            manageSignIn: accountRole === 'owner',
          },
          account: {
            id: 'account',
            username: 'fixture_member',
            primaryProfileId: 'profile',
            role: accountRole,
            revision: 1,
            disabled: false,
            allowedLibraries: ['movies'],
          },
          profiles: [
            {
              id: 'profile',
              name: 'Fixture profile',
              primary: true,
              position: 0,
              art: 'mint',
              revision: 1,
              pinRevision: 1,
              pinRequired: false,
            },
          ],
        },
      };
    if (path === routePath('get_direct_profile_avatars'))
      return {status: 200, body: {items: []}};
    if (path === routePath('get_guide_sources'))
      return {
        status: 200,
        body: {
          protocolVersion: '1.0',
          serverId: pin.serverId,
          viewerFence: 'guide-fence',
          revision: 'guide-1',
          sources: [],
        },
      };
    if (path === routePath('get_notifications_inbox'))
      return {
        status: 200,
        body: envelope('inbox', {
          revision: 1,
          audience: 'profile',
          audiences: ['profile'],
          state: 'unread',
          counts: {unread: 0, read: 0, archived: 0, total: 0},
          items: [],
          nextCursor: '',
          observedAt: new Date().toISOString(),
          retentionDays: 180,
        }),
      };
    if (path === routePath('get_notifications_unread_count'))
      return {
        status: 200,
        body: envelope('inbox', {
          revision: 1,
          audience: 'profile',
          counts: {unread: 0, read: 0, archived: 0, total: 0},
          observedAt: new Date().toISOString(),
        }),
      };
    if (path === routePath('post_home_destination_picker'))
      return {
        status: 200,
        body: {
          serverId: pin.serverId,
          viewerFence: homeFence,
          kind: request.body.kind,
          query: request.body.query,
          revision: {catalog: 1, viewer: 1},
          rows: [],
          nextCursor: '',
        },
      };
    if (path === routePath('get_capabilities'))
      return {
        status: 200,
        body: {
          features: {
            playback_v1_kinds: 'movie,episode,song,audiobook_file',
            playback_v1_channels: 'enabled',
          },
        },
      };
    if (fault && path.includes('/playback-options'))
      return {
        status: fault.status,
        body: {
          error: {code: fault.code, message: 'Boundary playback failure'},
        },
      };
    if (/^\/v1\/items\/[^/]+$/.test(path))
      return {status: 200, body: homeEntry()};
    return backend.handle(request);
  };
  const mutationReceipts = new Map();
  state.modules.PorticoRuntime = {
    ...userMutationBridge(),
    playbackRequestId: () => randomUUID(),
    newRequestId: () => randomUUID(),
    playbackRandomBytes: n => [...randomBytes(n)],
    prepareSecureRecords: async () => false,
    profileInstallation: async () => installationId,
    loadSelectedViewer: async () => selected,
    saveSelectedViewer: async value => {
      selected = value === 'null' ? null : value;
    },
    loadQueueRecord: async key => store.get(key) ?? null,
    saveQueueRecord: async (key, value) => {
      store.set(key, value);
    },
    cancelQueueRequest: id => {
      cancellations.push(id);
      pending.get(id)?.abort();
    },
    networkRequest: async (id, url, method, headers, body) => {
      const controller = new AbortController();
      pending.set(id, controller);
      const request = {
        method,
        url,
        path: new URL(url).pathname + new URL(url).search,
        headers,
        body: body ? JSON.parse(body) : undefined,
        signal: controller.signal,
      };
      requests.push(request);
      try {
        const response = await reply(request);
        const mutation = userMutationRoute(request.path, method);
        const key = Object.entries(headers).find(([name]) => name.toLowerCase() === 'idempotency-key')?.[1];
        if (mutation && key && response.status >= 200 && response.status < 300) {
          mutationReceipts.set(key, {key, operation: mutation.id, status: 'applied', committedMs: Date.now(), expiresMs: Date.now() + 30 * 86400000});
        }
        receipts.push({
          path: request.path,
          method,
          status: response.status,
          errorCode: response.body?.error?.code,
          // A resumed Play starts its session inside the queue request
          // (startPlayback); an explicit start still posts its own session.
          sessionId: response.status === 201
            ? method === 'POST' && request.path === '/v1/playback/sessions'
              ? response.body?.id
              : response.body?.session?.id
            : undefined,
        });
        return {
          status: response.status,
          headers: {'Content-Type': 'application/json', ...response.headers},
          body: response.status === 204 ? '' : JSON.stringify(response.body),
        };
      } catch (error) {
        nativeErrors.push(error);
        throw error;
      } finally {
        pending.delete(id);
      }
    },
  };
  state.modules.PorticoRuntime.profileRequest =
    state.modules.PorticoRuntime.networkRequest;
  state.modules.PorticoNetwork = {
    sha256: async value => b64(digest(Buffer.from(value, 'base64url'))),
    verify: async (publicKey, signature, payload) =>
      verify(
        null,
        Buffer.from(payload, 'base64url'),
        createPublicKey({
          key: Buffer.concat([
            Buffer.from('302a300506032b6570032100', 'hex'),
            Buffer.from(publicKey, 'base64url'),
          ]),
          format: 'der',
          type: 'spki',
        }),
        Buffer.from(signature, 'base64url'),
      ),
    startMonitoring() {},
    browse: async () => [],
    cancelBrowse() {},
  };
  state.modules.PorticoCapabilities = {
    facts: async () => ({
      deviceName: 'Native boundary phone',
      appVersion: 'r25-test',
      platform: 'android',
      sdk: 36,
      model: 'boundary',
      decoders: [],
    }),
  };
  state.modules.PorticoHaptics = {selection: async () => {}};
  state.modules.PorticoPlaybackModule = {
    features: {},
    acquire: async () => ({
      lease: 'lease-' + ++lease,
      instanceId: 'instance-' + lease,
    }),
    release: async () => {},
    command: async value => {
      commands.push(value);
    },
  };
  const exports = {},
    privateInterface = {
      ReactNativeViewConfigRegistry: {get: () => ({})},
      createPublicRootInstance: () => ({}),
      RawEventEmitter: {emit() {}},
      ReactFiberErrorDialog: {showErrorDialog: () => true},
    },
    fabric = {
      createNode: (tag, name, root, props) => ({
        tag,
        name,
        root,
        props,
        children: [],
      }),
      appendChild: (node, child) => node.children.push(child),
      createChildSet: () => [],
      appendChildToSet: (set, child) => set.push(child),
      completeRoot() {},
      unstable_getCurrentEventPriority: () => 32,
    };
  const rendererPath = require.resolve(
    'react-native/Libraries/Renderer/implementations/ReactFabric-prod.js',
  );
  compileFunction(readFileSync(rendererPath, 'utf8'), [
    'require',
    'exports',
    'nativeFabricUIManager',
  ])(
    name =>
      name.endsWith('ReactNativePrivateInitializeCore')
        ? {}
        : name.endsWith('ReactNativePrivateInterface')
        ? privateInterface
        : name === 'react'
        ? React
        : require(name),
    exports,
    fabric,
  );
  if (setup) await import('../../setup-sign-in-r25.tsx');
  else await import('../../index.js');
  const App = state.registrations.get('Portico')?.();
  if (!App) throw Error('Standard index did not register Portico');
  const subscriptions = () => ({
    listeners: [...state.events.values()].reduce(
      (total, listeners) => total + listeners.size,
      0,
    ),
    appState: state.appState.size,
    pending: pending.size,
    frames: state.frames.size,
  });
  const baseline = subscriptions(),
    errors = [];
  const settle = async (count = 25) => {
    for (let n = 0; n < count; n++)
      await new Promise(done => setTimeout(done, 0));
    if (errors.length) throw errors[0];
  };
  const mount = async (appProps = {}) => {
    exports.render(React.createElement(App, appProps), 77, undefined, true, {
      onUncaughtError: error => errors.push(error),
      onCaughtError: error => errors.push(error),
      onRecoverableError: error => errors.push(error),
    });
    await settle();
  };
  if (mountUI) await mount();
  const card = () => {
    const value = [...state.hosts.values()].find(
      host =>
        host.name === 'Pressable' &&
        host.props.accessibilityLabel?.startsWith('Native boundary test movie'),
    );
    if (!value) throw Error('No actual native Continue card host');
    return value;
  };
  return {
    requests,
    receipts,
    setDeleteCapability: value => {
      deleteCapability = value;
    },
    // CC-03: the server changes this account's role between reads.
    setAccountRole: value => {
      accountRole = value;
    },
    // Synthetic native store only; never connected to device authentication.
    syntheticStorage: () => ({
      selected: selected ? JSON.parse(selected) : null,
      connection: store.has('network.selected.v1')
        ? JSON.parse(store.get('network.selected.v1'))
        : null,
    }),
    resetSyntheticStorage: () => {
      selected = null;
      store.clear();
      setInstallationIdentity(undefined);
    },
    async fill(label, value) {
      const target = [...state.hosts.values()].find(
        host =>
          host.name === 'TextInput' && host.props.accessibilityLabel === label,
      );
      if (!target) throw Error('Missing native input: ' + label);
      target.props.onChangeText(value);
      await settle();
    },
    commands,
    backend,
    errors,
    nativeErrors,
    hosts: () => [...state.hosts.values()],
    nativeLabel,
    settle,
    homeDoc,
    card,
    mount,
    baseline,
    fault: value => {
      fault = value;
    },
    refresh: async () => {
      homeVersion++;
      for (const listener of [...state.appState]) listener('active');
      await settle();
    },
    async tap() {
      card().props.onPress({nativeEvent: {}, stopPropagation() {}});
      await settle();
    },
    async select() {
      card().props.onAccessibilityAction({
        nativeEvent: {actionName: 'select'},
      });
      await settle();
    },
    async tapLabel(label) {
      const target = [...state.hosts.values()].find(
        value => value.name === 'Pressable' && nativeLabel(value) === label,
      );
      if (!target) throw Error('Missing native host label: ' + label);
      target.props.onPress({nativeEvent: {}, stopPropagation() {}});
      await settle();
    },
    async back() {
      for (const listener of [...state.back].reverse()) if (listener()) break;
      await settle();
    },
    async more() {
      card().props.onAccessibilityAction({
        nativeEvent: {actionName: 'more'},
      });
      await settle();
    },
    async menuPlay() {
      const button = [...state.hosts.values()].find(
        host =>
          host.name === 'Pressable' &&
          host.props.accessibilityLabel === 'Play',
      );
      if (!button)
        throw Error('Real Home registered phone menu did not offer Play');
      button.props.onPress({nativeEvent: {}, stopPropagation() {}});
      await settle();
    },
    /** H-11: the next Home read answers these fences (a library change, or a privacy change). */
    async changeHome(fences) {
      homeFence = fences.viewerFence ?? homeFence;
      if ('privacyFence' in fences) homePrivacy = fences.privacyFence;
      for (const listener of [...state.appState]) listener('active');
      await settle();
    },
    async retireHome() {
      homeFence = 'retired-home-fence';
      for (const listener of [...state.appState]) listener('active');
      await settle();
    },
    async unmount() {
      // A recorded render failure is the test's result, so this app's own frames
      // and install identity are always released before that same error is rethrown.
      try {
        exports.stopSurface(77);
        await settle();
      } finally {
        for (const timer of state.frames.values()) clearImmediate(timer);
        state.frames.clear();
        setInstallationIdentity(undefined);
      }
    },
    disposeLoader: () => {
      loader.deregister();
      for (const handle of timeouts) before.clearTimeout(handle);
      for (const handle of intervals) before.clearInterval(handle);
      timeouts.clear();
      intervals.clear();
      globalThis.__DEV__ = before.dev;
      globalThis.requestAnimationFrame = before.frame;
      globalThis.cancelAnimationFrame = before.cancelFrame;
      globalThis.setTimeout = before.timeout;
      globalThis.clearTimeout = before.clearTimeout;
      globalThis.setInterval = before.interval;
      globalThis.clearInterval = before.clearInterval;
      console.warn = before.warn;
      console.error = before.error;
      delete globalThis[globalKey];
    },
    subscriptions,
  };
}
