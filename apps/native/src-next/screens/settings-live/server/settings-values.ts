import type {SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';

/** A half-entered number stays in its field; it must never overwrite a valid draft. */
export function settingNumber(text: string, control: Pick<SettingsRow['control'], 'optional' | 'min' | 'max' | 'step'>): Readonly<{valid: boolean; value?: number | null}> {
  if (!text.trim()) return control.optional ? {valid: true, value: null} : {valid: false};
  if (!/^-?(?:\d+(?:\.\d*)?|\.\d+)$/.test(text.trim())) return {valid: false};
  const value = Number(text), min = control.min ?? 0, max = control.max ?? Number.MAX_SAFE_INTEGER;
  if (!Number.isFinite(value) || value < min || value > max) return {valid: false};
  const step = control.step ?? 1, steps = (value - min) / step;
  if (step <= 0 || Math.abs(steps - Math.round(steps)) > 1e-8) return {valid: false};
  return {valid: true, value};
}

export function settingEnabled(row: SettingsRow, read: (id: string) => unknown): boolean {
  const condition = row.enableWhen;
  if (!condition) return true;
  const value = read(condition.id);
  if (value === undefined) return false;
  return (!condition.in || condition.in.some(candidate => Object.is(candidate, value)))
    && (!condition.notIn || !condition.notIn.some(candidate => Object.is(candidate, value)));
}

export function settingRows(rows: readonly SettingsRow[]): readonly SettingsRow[] {
  return rows.flatMap(row => [row, ...settingRows(row.children ?? [])]);
}

/** Never round an exact server value into the nearest named choice. */
export function settingChoice(row: SettingsRow, value: unknown): string {
  return row.control.options?.some(option => String(option.value) === String(value)) ? String(value) : 'custom';
}

function sameSetting(a: unknown, b: unknown): boolean {
  if (Object.is(a, b) || a != null && b != null && typeof a !== 'object' && typeof b !== 'object' && String(a) === String(b)) return true;
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object' || Array.isArray(a) !== Array.isArray(b)) return false;
  const left = a as Record<string, unknown>, right = b as Record<string, unknown>;
  return Object.keys(left).length === Object.keys(right).length && Object.keys(left).every(key => Object.hasOwn(right, key) && sameSetting(left[key], right[key]));
}

export function settingsSummary(rows: readonly SettingsRow[], read: (row: SettingsRow) => Readonly<{current: unknown; default?: unknown}> | undefined, translate: (id: SettingsRow['label']) => string): string {
  const parts: string[] = [];
  for (const row of settingRows(rows)) {
    if (row.noChanged || !['select', 'toggle', 'number', 'text', 'matrix'].includes(row.control.type)) continue;
    const fact = read(row), fallback = row.control.default ?? (row.control.type === 'matrix' ? Object.fromEntries((row.control.items ?? []).filter(item => item.id).map(item => [item.id!, item.default])) : undefined);
    const published = fact?.default, incomplete = published && typeof published === 'object' && Object.values(published).some(value => value === undefined);
    const expected = published === undefined || incomplete ? fallback : published;
    if (!fact || fact.current === undefined || expected === undefined || sameSetting(fact.current, expected)) continue;
    const option = row.control.options?.find(value => String(value.value) === String(fact.current));
    const shown = option ? translate(option.label) : typeof fact.current === 'boolean' ? fact.current ? translate('web.dvr.on') : translate('settings.server.secure.badgeOff') : typeof fact.current === 'object' ? translate('server.channels.custom') : String(fact.current);
    parts.push(`${translate(row.label)}: ${shown}`);
  }
  return parts.length ? parts.join(' · ') : translate('next.settings.allDefaults');
}

export function missingRemovalTime(row: Readonly<{missingSince: string; removedAt?: string}>, rule: string | undefined, days: number | undefined): number | undefined {
  if (row.removedAt) {
    const removed = Date.parse(row.removedAt);
    return Number.isFinite(removed) ? removed : undefined;
  }
  const since = Date.parse(row.missingSince);
  if (!Number.isFinite(since) || rule === 'never' || rule === undefined) return undefined;
  if (rule === 'immediately') return since;
  if (rule !== 'after_days' || !Number.isSafeInteger(days) || (days ?? 0) < 1) return undefined;
  return since + days! * 86_400_000;
}

export function downloadPerson(row: Readonly<{accountName?: string; profileName?: string}>): Readonly<{name: string; profile?: string}> {
  return {name: row.accountName || '—', ...(row.profileName && row.profileName !== row.accountName ? {profile: row.profileName} : {})};
}
