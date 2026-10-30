/** Step through the actual returned search hits. With no selected hit, Next
 * starts at the first result and Previous starts at the last. */
export function readingSearchStepIndex(count: number, current: number, direction: 'next' | 'previous'): number | undefined {
  if (count <= 0) return undefined;
  if (current < 0 || current >= count) return direction === 'next' ? 0 : count - 1;
  return (current + (direction === 'next' ? 1 : -1) + count) % count;
}
