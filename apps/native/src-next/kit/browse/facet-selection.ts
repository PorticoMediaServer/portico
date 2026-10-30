export function nextFacetState(value: string, included: readonly string[], excluded: readonly string[], cycleExclusion: boolean): 'include' | 'exclude' | 'clear' {
  if (excluded.includes(value)) return 'clear';
  if (included.includes(value)) return cycleExclusion ? 'exclude' : 'clear';
  return 'include';
}

export function changeFacetSelection(value: string, state: 'include' | 'exclude' | 'clear', included: readonly string[], excluded: readonly string[]): Readonly<{included: readonly string[]; excluded: readonly string[]}> {
  return {
    included: state === 'include' ? [...included.filter(x => x !== value), value] : included.filter(x => x !== value),
    excluded: state === 'exclude' ? [...excluded.filter(x => x !== value), value] : excluded.filter(x => x !== value),
  };
}
