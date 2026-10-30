/** TV linking accepts the eight letters/digits printed on the requesting TV. */
export function formatTVLinkCode(value: string): string {
  const code = value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  return code.length > 4 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

export function completeTVLinkCode(value: string): boolean {
  return /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(value);
}

/** Match the server's 64-label, 120-character, case-insensitive uniqueness limits. */
export function addHiddenLabel(labels: readonly string[], value: string): readonly string[] | undefined {
  const label = value.trim();
  if (!label || label.length > 120 || /[\x00-\x1f\x7f]/.test(label) || labels.length >= 64
    || labels.some(current => current.toLowerCase() === label.toLowerCase())) return undefined;
  return [...labels, label];
}
