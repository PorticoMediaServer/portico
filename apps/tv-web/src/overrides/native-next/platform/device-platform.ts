// The TV-web counterpart of `apps/native/src-next/platform/device-platform.ts`: what this set is,
// for everything that has to name it. Every screen, sign-in request and report reads it here.
import type {MessageId} from '../../../../../../packages/i18n/src/index';
import type {DestinationPlatform} from '../../../../../../packages/client-core/src/presentation/destinations';
import type {DevicePlatform} from '../../../../../native/src-next/platform/device-platform';
import {deviceKindName, deviceNoun as noun, platformId} from '../../../platform/identity';

export type {DevicePlatform};

/** A television, never a tablet. */
export const isTablet: boolean = false;

/** The platform word servers and the Portico Account record: `webos` or `tizen`. */
export const devicePlatform: DevicePlatform = platformId;
export const installationPlatform: DevicePlatform | 'ipados' = devicePlatform;

/** The capability document is this app's (`src/capabilities`), not Android's. */
export const androidFamily: 'android' | 'androidtv' | 'firetv' | undefined = undefined;

/** What to call the set when it has no name of its own. A product name; not translated. */
export const deviceModelName: string = deviceKindName;

/** The device noun in copy: "this LG TV", "this Samsung TV". */
export const deviceNoun: MessageId = noun;

/** "Play on" is asked as for any television: it plays what it is sent and sends nothing on. */
export const playOnPlatform: DestinationPlatform = platformId;

/** No AirPlay and no Picture in Picture in a TV app. */
export const hasAirPlay: boolean = false;
export const hasPictureInPicture: boolean = false;

/** What the TV shows and plays is asked of the set where it can say (`src/capabilities`); the
 * viewer can still say it in Settings (HDR, surround) where the set cannot. */
export const declaresOutputs: boolean = true;
