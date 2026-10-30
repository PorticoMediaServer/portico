import React, {useMemo} from 'react';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {SortMenu} from '../../kit/browse';

type Sort = Readonly<{id: string; labelKey: string; directions: readonly ('asc' | 'desc')[]}>;

/** The shared field and direction picker over exactly the Saved projection's offers. */
export function SavedSortMenu({sorts, sort, direction, onChange, scope, label, focusId}: {
  sorts: readonly Sort[]; sort?: string; direction?: string; scope: string; focusId?: string;
  /** Names an offer the catalogue's label keys do not (a resource's own order). */
  label?: (sort: Sort) => string | undefined;
  onChange: (sort: string, direction: 'asc' | 'desc') => void;
}) {
  const offers = useMemo(() => sorts.map(value => ({...value, defaultDirection: value.directions[0] ?? 'asc' as const,
    expensive: false, applicableKinds: []})), [sorts]);
  const active = offers.find(value => value.id === sort) ?? offers[0];
  if (!active) return null;
  const order = direction === 'desc' || direction === 'asc' ? direction : active.defaultDirection;
  return <SortMenu scope={scope} focusId={focusId} sorts={offers} value={{field: active.id, direction: active.directions.includes(order) ? order : active.defaultDirection,
    flippable: active.directions.length > 1, reshufflable: false}}
    label={value => label?.(value) ?? (defaultI18n.has(value.labelKey as MessageId) ? defaultI18n.t(value.labelKey as MessageId) : value.id)}
    onValueChange={value => {const offered = offers.find(choice => choice.id === value.field);
      if (offered?.directions.includes(value.direction)) onChange(value.field, value.direction);}}/>;
}
