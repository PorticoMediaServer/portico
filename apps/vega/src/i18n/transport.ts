import {createI18n, resolveRegion} from '../../../../packages/i18n/src/index';

type TransportNotice = 'vega.connection.redirectLimit' | 'vega.connection.credentialExposed';

/** The transport's two notices from the shared catalogue, in the viewer's language. */
export function transportMessage(id: TransportNotice, locale = 'en-US'): string {
  return createI18n(resolveRegion({locale}, {locales: [locale]})).t(id);
}
