import {Dimensions, NativeModules, Platform} from 'react-native';
import type {MessageId} from '../../../../packages/i18n/src/index';
import type {DestinationPlatform} from '../../../../packages/client-core/src/presentation/destinations';

/**
 * What this device is, for everything that has to name it: the platform word the servers
 * record, the model an approval screen falls back to, the noun Settings uses, and whether a
 * handheld is a tablet. Screens never ask the operating system; they ask this.
 */
const android = Platform.OS === 'android';
const amazon = android && String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon';

/** A tablet-class handheld: an iPad, or an Android device at least 600 points across its short side. */
export const isTablet: boolean = !Platform.isTV && (android
  ? Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600
  : (Platform as {isPad?: boolean}).isPad === true);

/** `vega` is a Fire TV on Vega OS: only the Vega app announces it (its own `device-platform.ts`). */
export type DevicePlatform = 'tvos' | 'ios' | 'androidtv' | 'firetv' | 'android' | 'vega';

/** The platform a device announces itself as. Fire TV is Android TV on Amazon hardware. */
export const devicePlatform: DevicePlatform = Platform.isTV ? (amazon ? 'firetv' : android ? 'androidtv' : 'tvos') : android ? 'android' : 'ios';

/** The same, for an installation's identity, where an iPad says so. */
export const installationPlatform: DevicePlatform | 'ipados' = devicePlatform === 'ios' && isTablet ? 'ipados' : devicePlatform;

/** The capability profile's client family (client-core `androidProfile`), or undefined on Apple. */
export const androidFamily: 'android' | 'androidtv' | 'firetv' | undefined = android ? (Platform.isTV ? (amazon ? 'firetv' : 'androidtv') : 'android') : undefined;

/** What to call the device when it has no name of its own. Product names; not translated. */
export const deviceModelName: string = Platform.isTV
  ? (amazon ? 'Fire TV' : android ? 'Android TV' : 'Apple TV')
  : android ? (isTablet ? 'Android tablet' : 'Android phone') : isTablet ? 'iPad' : 'iPhone';

/** The device noun in copy: "this iPhone", "this tablet", "this Fire TV". */
export const deviceNoun: MessageId = Platform.isTV
  ? (amazon ? 'device.noun.fireTV' : android ? 'device.noun.androidTV' : 'device.noun.appleTV')
  : android ? (isTablet ? 'device.noun.tablet' : 'device.noun.phone') : isTablet ? 'device.noun.iPad' : 'device.noun.iPhone';

/** The platform "Play on" is offered for (client-core `playOnDestinations`). */
export const playOnPlatform: DestinationPlatform = devicePlatform === 'ios' ? (isTablet ? 'ipados' : 'ios') : devicePlatform;

/** The system AirPlay picker exists on iPhone and iPad only. */
export const hasAirPlay: boolean = !android && !Platform.isTV;

/** Picture in Picture: iPhone and iPad, and Android phones and tablets. Never a television. */
export const hasPictureInPicture: boolean = !Platform.isTV && NativeModules.PorticoRuntime?.pictureInPictureSupported === true;

/** Whether the viewer says what the TV shows and plays (HDR, surround) because the device cannot
 * tell. Apple and Android detect them; Vega says yes. */
export const declaresOutputs: boolean = false;
