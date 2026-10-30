import {NativeModules, Platform} from 'react-native';

// Android: the runtime says which layout this process was started with: the person's Layout
// setting when they made one, otherwise what the device reports (a television flavor, Leanback,
// television mode, or a box with no touchscreen). React Native's own answer follows it, so a box
// that does not report "television" gets the television layout, and a device the person set to
// Touch gets the touch layout. Run before any app modules load (B-19.10).
const television = Platform.OS === 'android' ? (NativeModules.PorticoRuntime as {television?: unknown} | undefined)?.television : undefined;
if (television === true) (Platform.constants as {uiMode?: string}).uiMode = 'tv';
else if (television === false && (Platform.constants as {uiMode?: string}).uiMode === 'tv') (Platform.constants as {uiMode?: string}).uiMode = 'normal';
