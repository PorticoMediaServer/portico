import type {BrowseCapabilities, BrowseNode, BrowseOperator} from '../../../../../packages/client-core/src/browse';
import {appliedFilterLabel} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {BrowseLabels} from '../../kit/browse';

/** One chip keeps its actual whole predicate identity. Unsupported expression
 * trees remain in the query and in the full Filters editor; never flatten OR. */
export function libraryAppliedFilters(nodes: readonly BrowseNode[], capabilities: BrowseCapabilities, labels: BrowseLabels) {
  const result = new Map<string, Readonly<{id: string; label: string; fieldName: string}>>();
  const inverse: Partial<Record<BrowseOperator, BrowseOperator>> = {equals: 'not-equals', 'not-equals': 'equals', in: 'not-in', 'not-in': 'in',
    'contains-any': 'not-in', 'is-present': 'is-missing', 'is-missing': 'is-present', 'less-than': 'at-least', 'at-least': 'less-than', 'greater-than': 'at-most', 'at-most': 'greater-than'};
  for (const node of nodes) {
    const leaf = 'field' in node ? node : 'not' in node && 'field' in node.not && inverse[node.not.operator] ? {...node.not, operator: inverse[node.not.operator]!} : undefined;
    if (!leaf) continue;
    const field = capabilities.fields.find(value => value.id === (leaf.field === 'decade' ? 'year' : leaf.field)) ?? capabilities.fields.find(value => value.id === leaf.field);
    if (!field) continue;
    const id = JSON.stringify(node), fieldName = labels.field(field);
    result.set(id, {id, fieldName, label: appliedFilterLabel(leaf, () => fieldName, defaultI18n.t)});
  }
  return [...result.values()];
}
