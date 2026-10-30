import type {CardProps} from './types';

/** Canonical copy only; decorative text runs and artwork never replace facts. */
export function cardDescription({model, extraCaption}: Pick<CardProps, 'model' | 'extraCaption'>, status: readonly (string | undefined)[] = []): string {
  const facts = [...model.caption, ...(model.detail ?? []), ...(extraCaption?.flat() ?? [])].map(part => part.text);
  return [...new Set([...facts, ...status].filter((text): text is string => typeof text === 'string' && text.length > 0))].join(', ');
}
