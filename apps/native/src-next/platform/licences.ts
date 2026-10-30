import {Platform} from 'react-native';
import {parseLicenceData, type LicenceData} from '../../../../packages/client-core/src/presentation/licences';

/** The generated build inventory is loaded only when the notices screen opens. */
export function loadLicences(): LicenceData | undefined {return parseLicenceData(require('../../licences/generated/apple.json'));}
export function licenceVariant(): string {return Platform.isTV ? 'tvos' : 'ios';}
