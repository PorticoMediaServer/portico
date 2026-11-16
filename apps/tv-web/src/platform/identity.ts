import {serviceText} from '../../../../packages/client-core/src/presentation/service-text';
import type {MessageId} from '../../../../packages/i18n/src/index';
import {tvPlatform} from './device';

/**
 * How this app names its platform to servers and to people: `webos` on an LG set and `tizen` on
 * a Samsung one, the words the server's playback profiles and the Portico Account already use.
 */
export const platformId = tvPlatform;
/** The device noun in copy: "this LG TV", "this Samsung TV". */
export const deviceNoun: MessageId = tvPlatform === 'tizen' ? 'device.noun.samsungTV' : 'device.noun.lgTV';
/** What an approval screen calls this device when it has no name of its own. */
export const deviceKindName: string = serviceText(deviceNoun);
