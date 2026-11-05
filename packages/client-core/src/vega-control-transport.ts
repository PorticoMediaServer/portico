/** The SDK appends each entry, so retain order/casing and the final no-store
 * entry even when a caller already supplied Cache-Control. Do not merge it. */
export function vegaControlHeaderEntries(headers: Readonly<Record<string, string>>): ReadonlyArray<readonly [string, string]> {
  return [...Object.entries(headers), ['Cache-Control', 'no-store']];
}
