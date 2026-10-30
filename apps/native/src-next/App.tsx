import React, {useMemo, useRef, type ComponentType} from 'react';
import {Platform, StatusBar, View} from 'react-native';
import {GestureHandlerRootView} from 'react-native-gesture-handler';
import {SafeAreaProvider} from 'react-native-safe-area-context';
import {KeyboardProvider} from './platform/keyboard';
import {ThemeProvider, useTheme} from './kit/theme';
import {NativeShell, type NativeShellRuntime} from './shell';
import {RestoredShell} from './shell/RestoredShell';
import {AppErrorBoundary} from './shell/AppErrorBoundary';
import {reportFirstFrame} from './platform/boot-host';

if (!Platform.isTV) {
  // Native accounts routes register as a module side effect. Metro's inline
  // requires defer RestoredShell's import until its error branch, so the ready
  // shell would have no profiles/servers renderer. Require the module eagerly.
  require('./screens/accounts/AccountRoutes');
  require('./screens/downloads/register');
  require('./screens/collections/register');
  require('./screens/library/register');
  require('./screens/library/OverviewRoute');
  require('./screens/destinations/PinsRoute');
  require('./screens/destinations/music/register');
  require('./screens/destinations/audiobooks/register');
  require('./screens/read/register');
  require('./screens/people/register');
  require('./screens/notifications/register');
  require('./screens/search/register');
  const {registerNativeWatch, createNativeWatchBindings} = require('./screens/watch') as typeof import('./screens/watch');
  registerNativeWatch(createNativeWatchBindings);
}
require('./shell/settings-mount');
require('./screens/settings-live/LicencesRoute');
require('./screens/home/CustomizeRoute');
require('./screens/home/SeeAllRoute');
require('./screens/settings-live/LiveRoute');
require('./screens/settings-live/TogetherRoute');
require('./screens/saved/register');
const TVRoot: ComponentType | undefined = Platform.isTV ? require('./tv/register').TVRoot : undefined;

const KitHost: ComponentType<{shell?: ComponentType}> | undefined = __DEV__ ? require('./dev/Kit').KitHost : undefined;
const measurement: typeof import('./dev/public-measurement') | undefined = __DEV__ ? require('./dev/public-measurement') : undefined;
const measurementInstall: typeof import('./dev/measurement-install') | undefined = __DEV__ ? require('./dev/measurement-install') : undefined;
export type NativeAppProps = {phoneShell?: ComponentType; tvShell?: ComponentType; runtime?: NativeShellRuntime; reduceMotion?: boolean; textScale?: number};

function NativeRoot({phoneShell, tvShell, runtime}: NativeAppProps) {
  const {colors} = useTheme();
  const root = useRef<View>(null);
  const install = useMemo(() => (isCurrent: () => boolean) => measurementInstall!.installNativePublicMeasurement(() => root.current, isCurrent), []);
  const RuntimeShell = useMemo(() => runtime ? function BoundNativeShell() {return <NativeShell runtime={runtime}/>;} : undefined, [runtime]);
  const Shell = RuntimeShell ?? (Platform.isTV ? tvShell ?? TVRoot : phoneShell) ?? RestoredShell;
  const renderNormal = () => KitHost ? <KitHost shell={Shell} /> : Shell ? <Shell /> : null;
  const Gate = measurement?.NativePublicMeasurementGate;
  return <View ref={Gate ? root : undefined} collapsable={Gate ? false : undefined} testID="native-root" style={{flex: 1, backgroundColor: colors.ink}}><StatusBar barStyle="light-content" />{Gate ? <Gate renderNormal={renderNormal} install={install}/> : renderNormal()}</View>;
}

/** The product restores its actual native session; test/dev hosts may inject an owned runtime. */
export default function App(props: NativeAppProps) {
  // Whatever React draws first (Boot, a shell or the failure page) replaces the native boot layer.
  return <GestureHandlerRootView style={{flex: 1}} onLayout={reportFirstFrame}><SafeAreaProvider><KeyboardProvider><ThemeProvider reduceMotion={props.reduceMotion} textScale={props.textScale}><AppErrorBoundary><NativeRoot {...props} /></AppErrorBoundary></ThemeProvider></KeyboardProvider></SafeAreaProvider></GestureHandlerRootView>;
}
