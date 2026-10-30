// The Vega counterpart of `apps/native/src-next/platform/device-platform.ts`: what this device is, for
// everything that has to name it. The shared file tells an Apple TV from an Android television by
// the operating system React Native reports; a Fire TV on Vega OS is neither, so it is said here,
// once, and every screen, sign-in request and report reads it from this file.
import type {MessageId} from '../../../../../../packages/i18n/src/index';
import type {DestinationPlatform} from '../../../../../../packages/client-core/src/presentation/destinations';
import type {DevicePlatform} from '../../../../../native/src-next/platform/device-platform';
import {deviceKindName, platformId} from '../../../platform';

export type {DevicePlatform};

/** A television, never a tablet. */
export const isTablet: boolean = false;

/** The platform word servers and the Portico Account record for this device: `vega`. */
export const devicePlatform: DevicePlatform = platformId;
export const installationPlatform: DevicePlatform | 'ipados' = devicePlatform;

/** The capability document here is the Vega one (`bridge/capability-profile.ts`), not Android's. */
export const androidFamily: 'android' | 'androidtv' | 'firetv' | undefined = undefined;

/** What to call the device when it has no name of its own. A product name; not translated. */
export const deviceModelName: string = deviceKindName;

/** The device noun in copy: "this Fire TV". */
export const deviceNoun: MessageId = 'device.noun.fireTV';

/** "Play on" is asked as for any television: it plays what it is sent and sends nothing on. */
export const playOnPlatform: DestinationPlatform = platformId;

/** No AirPlay and no Picture in Picture on this device. */
export const hasAirPlay: boolean = false;
export const hasPictureInPicture: boolean = false;

/** Vega OS does not say what the TV shows or plays: the viewer does, in Settings (HDR, surround). */
export const declaresOutputs: boolean = true;
