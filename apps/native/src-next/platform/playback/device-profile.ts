import type {AndroidNativeFacts, AppleNativeFacts} from '../../../../../packages/client-core/src/client-profile';

/** What `PorticoCapabilities.facts()` answers: the platform's own shape, plus the device's name and the app's version. */
export type NativeFacts = (AppleNativeFacts | AndroidNativeFacts) & {deviceName?: string; appVersion?: string};
