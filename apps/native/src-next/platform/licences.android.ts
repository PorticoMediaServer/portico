import {Platform} from 'react-native';
import {parseLicenceData, type LicenceData} from '../../../../packages/client-core/src/presentation/licences';

export function loadLicences(): LicenceData | undefined {return parseLicenceData(require('../../licences/generated/android.json'));}
export function licenceVariant(): string {return Platform.isTV && String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'firetv' : 'google';}
