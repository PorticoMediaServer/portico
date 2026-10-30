/** Radix owns roving focus/selection. Keep its navigation out of player shortcuts. */
export function stopRadioNavigation(event: {key: string; stopPropagation(): void}) {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) event.stopPropagation();
}

/** Manual radios preview with focus; only native button activation commits. */
export function focusRadioChoice(event: {key: string; preventDefault(): void; stopPropagation(): void}, index: number, count: number, focus: (index: number) => void): boolean {
  const next = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? (index + 1) % count
    : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? (index - 1 + count) % count
    : event.key === 'Home' ? 0 : event.key === 'End' ? count - 1 : undefined;
  if (next === undefined) return false;
  event.preventDefault(); event.stopPropagation(); focus(next); return true;
}
