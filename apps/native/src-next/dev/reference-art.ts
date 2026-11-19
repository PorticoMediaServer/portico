import {Platform} from 'react-native';

/** Where the gallery's reference artwork is served (the mock-ups' `design/ui-v1/assets`, on mini-1 port 8787).
 * Android asks localhost: every Android device on mini-1 reaches it through `adb reverse tcp:8787 tcp:8787` (a phone on
 * USB cannot reach the Tailscale address, and the emulators load from localhost too), with the art server listening on
 * 127.0.0.1:8787. The Apple simulators on the Mac reach the Tailscale address. Development builds only. */
export const referenceOrigin = Platform.OS === 'android' ? 'http://127.0.0.1:8787' : 'http://100.120.50.24:8787';
