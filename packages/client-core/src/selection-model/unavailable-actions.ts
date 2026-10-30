import type {SelectionUnavailableActions} from './types.ts';

/** Closed wire vocabulary: an unrecognised restriction must never be dropped and
 * accidentally turn an unavailable whole-view action into a ready one. */
export function validSelectionUnavailableActions(value: unknown): value is SelectionUnavailableActions {
  if (!value || typeof value !== 'object' || Array.isArray(value)
    || ![Object.prototype, null].includes(Object.getPrototypeOf(value))) return false;
  return Object.entries(value).every(([key, reason]) =>
    (key === 'play' || key === 'queue' || key === 'playlist' || key === 'delete') ? reason === 'unsupported-sort'
      : key === 'metadataPreview' && reason === 'preview-limit');
}
