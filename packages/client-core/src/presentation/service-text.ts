/**
 * X-04 for services: the text a client-core service puts in its snapshot (`error.message`,
 * `notice`) comes from the catalogue, never from `error.message`. Apps set the viewer's catalogue
 * once (`setServiceI18n`, from their region hook); until then messages are en-US.
 */
import {defaultI18n, type I18n, type MessageId, type MessageValues} from '../../../i18n/src/index.ts';
import {presentError, type ErrorContext, type ErrorOperation} from './errors.ts';

let active: I18n = defaultI18n;
export function setServiceI18n(i18n: I18n): void {
  active = i18n;
}
export function serviceI18n(): I18n {
  return active;
}
/** A catalogue message in the viewer's language. */
export function serviceText(id: MessageId, values?: MessageValues): string {
  return active.t(id, values);
}
/**
 * What went wrong, in words a person can act on: a failure the shared presenter recognizes (offline,
 * permission, conflict…) is said its way; anything else gets this operation's own message.
 */
export function serviceProblem(error: unknown, context: ErrorContext, fallback: MessageId, operation: ErrorOperation = 'action'): string {
  const own = (error as {messageId?: unknown} | null)?.messageId;
  if (typeof own === 'string' && active.has(own)) return active.t(own as MessageId);
  const p = presentError(error, context, {operation, i18n: active});
  return p.category === 'unknown' ? active.t(fallback) : p.body;
}
