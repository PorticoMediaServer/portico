import type {SettingsRow, SettingsSection} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {MessageId} from '../../../../../../packages/i18n/src';

export type TVServerFact = Readonly<{id: string; label: string; value: string; detail?: string}>;

/** The credential list reports its exact server-scoped total, so one page is enough. */
export async function tvServerCredentialCount(read: (cursor: string) => Promise<Readonly<{total: number}>>, current: () => boolean): Promise<number> {
  if (!current()) throw new Error('native_tv_server_read_ended');
  const page = await read('');
  if (!current()) throw new Error('native_tv_server_read_ended');
  if (!Number.isSafeInteger(page.total) || page.total < 0) throw new Error('invalid_api_key_total');
  return page.total;
}

/** Shared televisions display reviewed scalar values, never raw configuration
 * documents. Unknown options, secret controls and free-form fields stay out. */
export function tvServerSettingsFacts(section: SettingsSection, read: (id: string) => unknown, t: (id: MessageId) => string, number: (value: number) => string): readonly TVServerFact[] {
  const facts: TVServerFact[] = [];
  const visit = (row: SettingsRow) => {
    if (row.control.secret || row.platforms && !row.platforms.includes('tv')) return;
    const value = read(row.id), control = row.control;
    let shown: string | undefined;
    if (control.type === 'toggle' && typeof value === 'boolean') shown = t(value ? 'next.settings.on' : 'next.settings.off');
    else if ((control.type === 'select' || control.type === 'segmented') && control.options) {
      const option = control.options.find(option => option.value === value);
      if (option) shown = t(option.label);
    } else if (control.type === 'number' && typeof value === 'number' && Number.isFinite(value)) shown = number(value);
    else if (row.id === 'general.name' && typeof value === 'string' && value.length <= 120 && !/[\u0000-\u001f\u007f]/.test(value)) shown = value;
    if (shown !== undefined) facts.push({id: row.id, label: t(row.label), value: shown});
    if (shown !== undefined) row.children?.forEach(visit);
  };
  for (const group of [...(section.groups ?? []), ...(section.advanced?.groups ?? [])]) {
    if (!group.platforms || group.platforms.includes('tv')) group.rows.forEach(visit);
  }
  return facts;
}
