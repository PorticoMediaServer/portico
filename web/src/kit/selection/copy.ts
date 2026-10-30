import {defaultI18n, formatMessage, parseMessage, type MessageValues} from '@i18n';
import {selectionMessages} from '../../../../packages/i18n/src/catalog/en-US/next/selection';

export type SelectionMessage = keyof typeof selectionMessages;

export function selectionText(key: SelectionMessage, values?: MessageValues): string {
  return formatMessage(selectionMessages[key], values, {locale: defaultI18n.region.formatLocale});
}

/** Preserve canonical ICU argument boundaries rather than searching the rendered title. */
export function selectionPickerTitleParts(key: 'next.selection.pickerTitle' | 'next.selection.newPickerTitle', values: Readonly<{items: string; destination: string}>) {
  return parseMessage(selectionMessages[key]).map(node => {
    if (node.kind === 'text') return {text: node.value};
    if (node.kind === 'arg' && (node.name === 'items' || node.name === 'destination')) return {argument: node.name, text: values[node.name]};
    throw new TypeError('Selection picker title requires text and named items/destination arguments.');
  });
}
