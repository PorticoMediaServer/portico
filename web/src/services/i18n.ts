import {createI18n, deviceRegion, resolveRegion} from '@i18n';
/** Account pages have no server/profile locale. Follow this device with source fallback. */
export const servicesI18n = () => createI18n(resolveRegion(undefined, deviceRegion()));
