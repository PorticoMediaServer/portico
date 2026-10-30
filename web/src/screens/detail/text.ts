import {defaultI18n} from '@i18n';
import {serviceI18n} from '@core/presentation/service-text.ts';

/** The shell updates the current catalogue from the actual region preference. */
export const t: typeof defaultI18n.t = (id, values) => serviceI18n().t(id, values);
export const locale = () => serviceI18n().locale;
export const environment = {t, get locale() { return locale(); }};
