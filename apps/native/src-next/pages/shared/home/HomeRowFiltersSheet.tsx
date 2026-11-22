import React from 'react';
import {FilterBuilder, FilterSheet} from '../../../kit/browse';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeHomeRowController} from '../../../screens/home/row-controller';
import {useHomeRowFilters} from './useHomeRowFilters';

/** A Home row's See all › Filters: the fields, and Show N results once the count is known. A side panel on a TV, a sheet on
 * touch. */
export function HomeRowFiltersSheet({controller, runtime, onClose}: {controller: NativeHomeRowController; runtime: NativeShellRuntime; onClose: () => void}) {
  const view = useHomeRowFilters({controller, runtime, onClose});
  if (!view) return null;
  return <FilterSheet title={view.title} testID="home-row-filters" onClose={onClose} showResults={view.showResults}>
    <FilterBuilder focusPrefix={view.focusPrefix} model={view.model} labels={view.labels} openFields={view.open} fields={view.fields}
      onFieldOpenChange={view.openChange} onFieldChange={view.change} onSearchChange={view.search} onRetry={view.retry}/>
  </FilterSheet>;
}
