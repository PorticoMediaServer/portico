import {parseMessage} from '../../../../../packages/i18n/src/index';

export type StateSentencePart = Readonly<{text: string; strong: boolean}>;
/** ICU argument boundaries preserve emphasis without searching or splitting rendered sentences. */
export function stateSentenceParts(source: string, values: Readonly<Record<string, string>>, emphasis: readonly string[]): readonly StateSentencePart[] {
  return parseMessage(source).map(node => {
    if (node.kind === 'text') return {text: node.value, strong: false};
    if (node.kind !== 'arg' || !Object.prototype.hasOwnProperty.call(values, node.name)) throw new TypeError('A state sentence needs its named text arguments.');
    return {text: values[node.name]!, strong: emphasis.includes(node.name)};
  });
}
