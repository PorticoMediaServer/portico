/** An opaque server witness. Clients validate its wire shape and carry it unchanged. */
export function validSelectionBasis(value: unknown): value is string {
  return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
}
