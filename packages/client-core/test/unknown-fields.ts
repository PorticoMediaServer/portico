/** A JSON answer as a newer server might send it: every object, at every depth, carries a field
 * this app has never heard of. A parser that reads the answer the same either way is safe against
 * a server that adds fields. */
export function withUnknownFields<T>(value: T): T {
  if (Array.isArray(value)) return value.map(withUnknownFields) as T;
  if (!value || typeof value !== 'object') return value;
  const copy = Object.fromEntries(Object.entries(value).map(([key, child]) => [key, withUnknownFields(child)]));
  return {...copy, addedByANewerServer: {since: '9.9'}} as T;
}
