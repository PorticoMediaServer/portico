import {useRef, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import type {BrowseCount, BrowseNode} from '@core/browse.ts';
import {Chip, RadioGroup, RadioItem} from '../../kit/controls';
import type {BrowseController, BrowseState} from './controller';
import {scopeTypes, type ScopeType} from './scope-types';
import styles from './browse.module.css';

/** Each chip is a separate server-evaluated count over the immutable Home row.
 * Multiple libraries of one kind are one query; their counts are never summed. */
export function BrowseScopeChips({controller, libraries, reportError, labelFor}: Readonly<{controller: BrowseController; libraries?: readonly Library[]; reportError: (error: unknown) => void; labelFor?: (kind: string) => string}>) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const [scope, setScope] = useState<Readonly<{owner: BrowseController; query: BrowseState; counts: readonly BrowseCount[]; signature: string}> | undefined>(undefined);
  const offered = !!libraries && controller.source.libraryId === '*' && state.capabilities?.fields.some(field => field.id === 'library' && field.operators.includes('in'));
  const signature = JSON.stringify({predicates: state.predicates.filter(node => !('field' in node) || node.field !== 'library'), groups: scopeTypes(libraries ?? [])});
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({state});
  useLayoutEffect(() => {setupPorts.current = {state};});
  useLayoutEffect(() => {
    const {state} = setupPorts.current;
    if (!offered) return;
    const stop = new AbortController(), original = state;
    const current = () => !stop.signal.aborted && controller.acceptsQuery(original);
    const {predicates, groups}: {predicates: readonly BrowseNode[]; groups: readonly ScopeType[]} = JSON.parse(signature);
    const timer = setTimeout(() => {
      if (!current()) return;
      void (async () => {
        const queries = [{id: 'all', predicates}, ...groups.map(group => ({id: group.kind,
          predicates: [...predicates, controller.parseNode({field: 'library', operator: 'in', value: group.libraryIds})]}))];
        return controller.counts(queries, stop.signal);
      })().then(counts => {
        if (current()) setScope({owner: controller, query: original, counts, signature});
      }).catch(error => {if (current()) {setScope(before => before?.query === original ? undefined : before);reportError(error);}});
    }, 180);
    return () => {clearTimeout(timer);stop.abort();};
  }, [controller, offered, signature, reportError, state.queryRevision, state.capabilities]);
  if (!offered || !scope || scope.owner !== controller || scope.signature !== signature || !controller.acceptsQuery(scope.query)) return null;
  const groups = scopeTypes(libraries ?? []), all = scope.counts.find(count => count.id === 'all');
  if (!all) return null;
  const selected = state.predicates.find(node => 'field' in node && node.field === 'library');
  const values = selected && 'field' in selected && selected.operator === 'in' ? Array.isArray(selected.value) ? selected.value : [selected.value] : [];
  const checked = (group: ScopeType) => group.libraryIds.length === values.length && group.libraryIds.every(id => values.includes(id));
  const label = (count: BrowseCount) => count.totalIsLowerBound ? defaultI18n.t('next.browse.countAtLeast', {count: count.total}) : defaultI18n.number(count.total);
  const change = (libraryIds?: readonly string[]) => {
    try {controller.setQuery([...state.predicates.filter(node => !('field' in node) || node.field !== 'library'),
      ...(libraryIds ? [controller.parseNode({field: 'library', operator: 'in', value: libraryIds})] : [])], undefined, state);}
    catch (error) {reportError(error);}
  };
  return <><RadioGroup className={styles.scopeChips} orientation="horizontal" value={!selected ? 'all' : groups.find(checked)?.kind ?? ''} onValueChange={value => change(value === 'all' ? undefined : groups.find(group => group.kind === value)?.libraryIds)} aria-label={defaultI18n.t('next.browse.libraries')}><RadioItem value="all" asChild><Chip variant="quick" selected={!selected} count={label(all)}>{defaultI18n.t('next.controls.all')}</Chip></RadioItem>
    {groups.map(group => {
      const count = scope.counts.find(count => count.id === group.kind);
      return count && (count.total > 0 || checked(group)) ? <RadioItem key={group.kind} value={group.kind} asChild><Chip variant="quick" selected={checked(group)} count={label(count)}>{labelFor?.(group.kind) ?? defaultI18n.t(group.label)}</Chip></RadioItem> : null;
    })}</RadioGroup><span className={styles.scopeSeparator} aria-hidden="true"/></>;
}
