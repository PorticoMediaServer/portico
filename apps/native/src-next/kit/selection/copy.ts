import {formatMessage, type MessageValues} from '../../../../../packages/i18n/src/index';
import {selectionNativeMessages} from '../../../../../packages/i18n/src/catalog/en-US/next/selection-native';

export type SelectionMessageId = keyof typeof selectionNativeMessages;
export function selectionText(id: SelectionMessageId, values?: MessageValues): string {
  return formatMessage(selectionNativeMessages[id], values, {locale: 'en-US'});
}
