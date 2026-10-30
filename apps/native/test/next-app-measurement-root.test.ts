import assert from 'node:assert/strict';
import test from 'node:test';
import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {compileFunction} from 'node:vm';
import {hooks} from './helpers/screen-harness.mjs';

const require = createRequire(import.meta.url);
type Element = {type: unknown; props: {children: Element[]; [key: string]: unknown}};
const nodes = (element: Element): Element[] => [element, ...element.props.children.flatMap(child =>
  child && typeof child === 'object' && 'props' in child ? nodes(child) : [])];

for (const dev of [true, false]) test(`actual App root disables collapsing only with the DEV measurement gate (${dev})`, async () => {
  const state = hooks();
  const jsx = (type: unknown, {children, ...props}: Record<string, unknown>) => state.react.createElement(type, props,
    ...(Array.isArray(children) ? children : children === undefined ? [] : [children]));
  const leaf = () => {throw new Error('root-prop test must not mount a shell, account screen or DEV gallery');};
  const file = new URL('../src-next/App.tsx', import.meta.url);
  const {code} = require('@babel/core').transformSync(await readFile(file, 'utf8'), {
    filename: file.pathname, configFile: false, babelrc: false,
    presets: [require.resolve('@react-native/babel-preset')],
    plugins: [require.resolve('@babel/plugin-transform-modules-commonjs')],
  });
  const reportFirstFrame = () => {};
  const modules: Record<string, unknown> = {
    react: state.react,
    'react/jsx-runtime': {jsx, jsxs: jsx},
    'react-native': {Platform: {isTV: false}, StatusBar: 'StatusBar', View: 'View'},
    'react-native-gesture-handler': {GestureHandlerRootView: 'GestureHandlerRootView'},
    'react-native-safe-area-context': {SafeAreaProvider: 'SafeAreaProvider'},
    './platform/keyboard': {KeyboardProvider: 'KeyboardProvider'},
    './kit/theme': {ThemeProvider: 'ThemeProvider', useTheme: () => ({colors: {ink: '#010203'}})},
    './shell/AppErrorBoundary': {AppErrorBoundary: 'AppErrorBoundary'}, './platform/boot-host': {reportFirstFrame},
    './shell': {NativeShell: leaf}, './shell/RestoredShell': {RestoredShell: leaf}, './shell/settings-mount': {},
    './screens/watch': {registerNativeWatch: (factory: unknown) => {assert.equal(factory, leaf);}, createNativeWatchBindings: leaf},
    './dev/Kit': {KitHost: leaf}, './dev/public-measurement': {NativePublicMeasurementGate: leaf},
    './dev/measurement-install': {installNativePublicMeasurement: () => {throw new Error('root-prop read must not install measurement');}},
  };
  const registrations = new Set(['./screens/accounts/AccountRoutes', './screens/downloads/register', './screens/collections/register', './screens/library/register', './screens/library/OverviewRoute',
    './screens/destinations/PinsRoute', './screens/destinations/music/register', './screens/destinations/audiobooks/register', './screens/read/register', './screens/people/register', './screens/notifications/register', './screens/search/register', './screens/home/CustomizeRoute', './screens/home/SeeAllRoute',
    './screens/settings-live/LicencesRoute', './screens/settings-live/LiveRoute', './screens/settings-live/TogetherRoute', './screens/saved/register']);
  const module = {exports: {} as {default?: (props: object) => Element}};
  compileFunction(code, ['require', 'exports', 'module', '__DEV__'], {filename: file.pathname})((name: string) => {
    if (name.startsWith('@babel/runtime/')) return require(name);
    if (registrations.has(name)) return {};
    assert.ok(Object.hasOwn(modules, name), `unexpected App dependency ${name}`);
    assert.ok(dev || !name.startsWith('./dev/'), 'production must not load a measurement or gallery module');
    return {__esModule: true, ...modules[name] as object};
  }, module.exports, module, dev);
  const App = module.exports.default;
  assert.ok(App);
  // React's first layout, whatever it draws, hands over from the native boot layer.
  assert.equal((App({}) as unknown as {props: {onLayout?: unknown}}).props.onLayout, reportFirstFrame);
  const roots = nodes(App({})).filter(element => typeof element.type === 'function');
  assert.equal(roots.length, 1, 'actual App exposes one NativeRoot under the existing providers');
  const root = state.render(() => (roots[0].type as (props: object) => Element)(roots[0].props));
  assert.equal(root.type, 'View');
  assert.equal(root.props.testID, 'native-root');
  assert.equal(root.props.collapsable, dev ? false : undefined);
  assert.equal(root.props.ref !== undefined, dev);
  assert.deepEqual(root.props.style, {flex: 1, backgroundColor: '#010203'});
  assert.equal(root.props.children.length, 2, 'no measurement wrapper or extra root was added');
});
