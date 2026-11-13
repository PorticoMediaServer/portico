import type {BrowseNode} from '@portico/client-core';

/**
 * An author facet value is the filter: the author's person id from the library's Authors list,
 * which the item list takes as `author=`. The same value the facet lists is what browse receives.
 */
export function authorPredicate(value: string): BrowseNode {
  if (!value) throw new Error('invalid_request');
  return {variant: 'predicate', field: 'author', operator: 'equals', value};
}
