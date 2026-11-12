/**
 * The names of the authors and series this app has listed, by their ids. A filter on an author or
 * a series sends the id; its chip says the name. Filled whenever a library's Authors or Series
 * list is read; bounded, so a very large library cannot grow it without end.
 */
const names = new Map<string, string>();
const kept = 5000;

export function rememberFilterNames(values: readonly Readonly<{value: string; label: string}>[]): void {
  for (const {value, label} of values) {
    names.delete(value);
    names.set(value, label);
    if (names.size > kept) names.delete(names.keys().next().value!);
  }
}

/** The name of an author or a series this app has listed, or undefined. */
export function filterValueName(value: unknown): string | undefined {
  return typeof value === 'string' ? names.get(value) : undefined;
}

/** The filters whose values are ids with a name to show. */
export const namedFilterFields: ReadonlySet<string> = new Set(['author', 'series']);
