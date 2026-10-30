import {parseLicenceData, type LicenceData} from '../../../../../../packages/client-core/src/presentation/licences';

/** Mounted native screens use Vega's own generated inventory, never Apple's notices. */
export function loadLicences(): LicenceData | undefined {return parseLicenceData(require('../../../licences/vega.json'));}
export function licenceVariant(): string | undefined {return undefined;}
