import type {BrowseNode} from '@portico/client-core';
export {loadReadingFacetPage as loadFacetPage} from '@core/query/reading-facets.ts';
export type {ReadingFacetField, FacetRequest, FacetPageOptions} from '@core/query/reading-facets.ts';

/**
 * An author facet value is the filter: the accepted attribution string, never a person identity
 * and never a parsed Subtitle. The same value the facet lists is what browse receives.
 */
export function authorPredicate(value: string): BrowseNode {
  if (!value) throw new Error('invalid_request');
  return {variant: 'predicate', field: 'author', operator: 'equals', value};
}
