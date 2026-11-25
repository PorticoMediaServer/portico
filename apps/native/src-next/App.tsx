import React, {useMemo, useRef, type ComponentType} from 'react';
import {Platform, View} from 'react-native';
import {GestureHandlerRootView} from 'react-native-gesture-handler';
import {SafeAreaProvider} from 'react-native-safe-area-context';
import {KeyboardProvider} from './platform/keyboard';
import {ThemeProvider, useTheme} from './kit/theme';
import {NativeShell, type NativeShellRuntime} from './shell';
import {registerNativeRoutes} from './shell/register-routes';
import {RestoredShell} from './shell/RestoredShell';
import {Layer} from './kit/shell/Layer';
import {AppErrorBoundary} from './shell/AppErrorBoundary';
import {reportFirstFrame} from './platform/boot-host';
import {detectLiteEffects} from './platform/motion-policy';
import './platform/server-clock-store';

void detectLiteEffects();

registerNativeRoutes(Platform.isTV);
const TVRoot: ComponentType | undefined = Platform.isTV ? require('./tv/register').TVRoot : undefined;

const KitHost: ComponentType<{shell?: ComponentType}> | undefined = __DEV__ ? require('./dev/Kit').KitHost : undefined;
const measurement: typeof import('./dev/public-measurement') | undefined = __DEV__ ? require('./dev/public-measurement') : undefined;
const measurementInstall: typeof import('./dev/measurement-install') | undefined = __DEV__ ? require('./dev/measurement-install') : undefined;
export type NativeAppProps = {phoneShell?: ComponentType; tvShell?: ComponentType; runtime?: NativeShellRuntime; reduceMotion?: boolean; textScale?: number};

function NativeRoot({phoneShell, tvShell, runtime}: NativeAppProps) {
  const root = useRef<View>(null);
  const install = useMemo(() => (isCurrent: () => boolean) => measurementInstall!.installNativePublicMeasurement(() => root.current, isCurrent), []);
  const RuntimeShell = useMemo(() => runtime ? function BoundNativeShell() {return <NativeShell runtime={runtime}/>;} : undefined, [runtime]);
  const Shell = RuntimeShell ?? (Platform.isTV ? tvShell ?? TVRoot : phoneShell) ?? RestoredShell;
  const renderNormal = () => KitHost ? <KitHost shell={Shell} /> : Shell ? <Shell /> : null;
  const Gate = measurement?.NativePublicMeasurementGate;
  return <View ref={Gate ? root : undefined} collapsable={Gate ? false : undefined} testID="native-root" style={{flex: 1}}>{Gate ? <Gate renderNormal={renderNormal} install={install}/> : renderNormal()}</View>;
}

/** The product restores its actual native session; test/dev hosts may inject an owned runtime. */
export default function App(props: NativeAppProps) {
  // Whatever React draws first (Boot, a shell or the failure page) replaces the native boot layer.
  // The app's root is the first layer: boot, sign-in, the account pages and the TV's own pages all stand on it.
  return <GestureHandlerRootView style={{flex: 1}} onLayout={reportFirstFrame}><SafeAreaProvider><KeyboardProvider><ThemeProvider reduceMotion={props.reduceMotion} textScale={props.textScale}><Layer><AppErrorBoundary><NativeRoot {...props} /></AppErrorBoundary></Layer></ThemeProvider></KeyboardProvider></SafeAreaProvider></GestureHandlerRootView>;
}
