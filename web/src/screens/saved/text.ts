import {defaultI18n, formatMessage, type MessageValues} from '@i18n';
import {savedMessages} from '../../../../packages/i18n/src/catalog/en-US/next/saved.ts';

/** Screen-owned source catalogue; canonical catalogue registration is steward-owned. */
export const savedText = (id: keyof typeof savedMessages, values?: MessageValues): string =>
  formatMessage(savedMessages[id], values, {locale: defaultI18n.region.formatLocale});
