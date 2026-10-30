import {serviceText} from '../../../packages/client-core/src/presentation/service-text';

/**
 * How this app names its platform to servers and to people. `overrides/native-next/platform/device-platform.ts`
 * puts these in place of what the shared code works out for an Apple or Android device.
 */
export const platformId = 'vega';
/** What an approval screen calls this device when it has no name of its own. */
export const deviceKindName = serviceText('device.noun.fireTV');
