import {defaultI18n, type MessageId} from '../../../i18n/src/index.ts';
import type {RepairFieldSpec} from '../metadata-repair.ts';

type Translate = (id: MessageId, values?: Record<string, string | number>) => string;
const translate: Translate = (id, values) => defaultI18n.t(id, values as never);

/** A metadata field's label in the app's words: the server's label is a catalogue key when the app knows it, else its own text. */
export function metadataFieldLabel(spec: RepairFieldSpec, t: Translate = translate): string {
  return defaultI18n.has(spec.label) ? t(spec.label as MessageId) : spec.label;
}

/** What is wrong with one field's value before Save, in the app's words, or '' when nothing is: a whole number within its
 * bounds, a real date, an allowed choice, a length within its limit, a title that is not empty. The same rule on every
 * platform (moved from the web editor so native and web check alike). */
export function metadataFieldError(spec: RepairFieldSpec, value: string, values: readonly string[] = [], requiredTitle = false, t: Translate = translate): string {
  const label = metadataFieldLabel(spec, t);
  if (requiredTitle && spec.field === 'title' && value.trim() === '') return t('web.metadata.titleRequired');
  if (spec.type === 'list') {
    if (spec.max !== undefined && values.length > spec.max) return t('web.metadata.tooManyEntries', {label, max: spec.max});
    if (spec.maxLength !== undefined && values.some(entry => Array.from(entry).length > spec.maxLength!)) return t('web.metadata.listEntryTooLong', {label, max: spec.maxLength});
    return '';
  }
  if (spec.type === 'integer' || spec.type === 'number') {
    // OW-3: an episode's season and number can't be cleared; say so beside the field, before Save, with its bounds.
    if (value.trim() === '') return spec.required ? spec.min !== undefined && spec.max !== undefined ? t('web.metadata.numberRange', {label, min: spec.min, max: spec.max}) : t('web.metadata.wholeNumber', {label}) : '';
    const number = Number(value);
    if (!Number.isSafeInteger(number) || number < 0 || String(number) !== value) return t('web.metadata.wholeNumber', {label});
    if (spec.min !== undefined || spec.max !== undefined) {
      if ((spec.min !== undefined && number < spec.min) || (spec.max !== undefined && number > spec.max)) return t('web.metadata.numberRange', {label, min: spec.min ?? '', max: spec.max ?? ''});
    }
    return '';
  }
  if (spec.type === 'date' && value) {
    const parsed = new Date(value + 'T00:00:00Z');
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value) || Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) return t('web.metadata.dateError', {label});
  }
  if (spec.type === 'enum' && value && spec.allowed && !spec.allowed.includes(value)) return t('web.metadata.enumError', {label});
  if (spec.maxLength !== undefined && Array.from(value).length > spec.maxLength) return t('web.metadata.textTooLong', {label, max: spec.maxLength});
  return '';
}
