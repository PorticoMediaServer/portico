/**
 * Server-authored labels (CON-19): a row or section title arrives as a catalogue message id, its
 * parameters and a US English fallback (`titleText`, or a content heading's `key`, `params` and
 * `fallback`). The catalogue message wins, in the viewer's language; the fallback stands in for a
 * code this client doesn't ship yet, and for a message whose parameters the server didn't send.
 */
import {enUS, messageArguments, type I18n, type MessageId} from '../../../i18n/src/index.ts';

export type ServerLabel = Readonly<{code?: string; key?: string; params?: Readonly<Record<string, string>>; fallback: string}>;

export function serverTextLabel(i18n: Pick<I18n, 't' | 'has'>, label: ServerLabel | undefined, fallback = ''): string {
  if (!label) return fallback;
  const code = label.code ?? label.key ?? '';
  if (code && i18n.has(code)) {
    const params = label.params ?? {};
    if (messageArguments(enUS[code as MessageId]).every(name => typeof params[name] === 'string')) {
      const text = i18n.t(code as MessageId, params);
      if (text) return text;
    }
  }
  return label.fallback || fallback;
}
