import type {SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {readSettingsBinding, settingsBinding, type SettingsBindingSources, type SettingsBindingValue} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, type SettingsGroup, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {ServerFormId} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {settingEnabled} from './settings-values';
import type {TranscodeCapacity} from '../../../../../../packages/client-core/src/telemetry';
import {transcodeLocationChoice} from '../../../../../../packages/client-core/src/presentation/transcode-location';
import {hardwareSelectionChoices} from '../../../../../../packages/client-core/src/presentation/hardware-selection';

/** The Server pages a TV owner edits through the same revisioned forms as the phone. */
export type TVEditablePage = 'general' | 'history' | 'libraries' | 'segments' | 'people' | 'integrations' | 'streaming' | 'storage' | 'remote';
export const tvPageForms: Readonly<Record<TVEditablePage, readonly ServerFormId[]>> = {
  general: ['runtime', 'maintenance', 'alerts'],
  history: ['runtime'],
  libraries: ['runtime'],
  segments: ['runtime'],
  people: ['runtime'],
  integrations: ['runtime'],
  streaming: ['runtime', 'connectivity'],
  storage: ['runtime', 'maintenance'],
  remote: ['connectivity', 'certificate'],
};
/** The configuration fact a derived choice is checked against (settings-bindings' own aliases). */
const policyId = (id: string) => id === 'general.updateChecks' ? 'general.updateFeed' : id === 'streaming.awayQuality' ? 'streaming.awayExact' : id === 'storage.backupKeep' ? 'storage.backupKeepExact' : id;

/** `plain` labels are already words (an adapter's driver name), never catalogue ids. */
export type TVChoiceModel = Readonly<{value: string; label: MessageId | string; hint?: MessageId | string; disabled?: boolean; plain?: boolean}>;
export type TVSettingItem = Readonly<{id: string; row: SettingsRow; dependent: boolean} & (
  | {kind: 'switch'; checked: boolean}
  | {kind: 'choice'; value: string; options: readonly TVChoiceModel[]}
  | {kind: 'number' | 'text'; value: string}
  | {kind: 'secret'; configured: boolean}
  | {kind: 'folder'; value: string}
  | {kind: 'matrix'; values: Readonly<Record<string, unknown>>; items: readonly Readonly<{id: string; label: MessageId; value: string}>[]; columns: readonly TVChoiceModel[]}
)>;
export type TVSettingGroup = Readonly<{title?: MessageId; items: readonly TVSettingItem[]}>;

export type TVServerSettingsState = Readonly<{
  page: TVEditablePage;
  configuration?: SettingsConfiguration;
  drafts: Readonly<Partial<Record<ServerFormId, Readonly<Record<string, unknown>>>>>;
  /** Rows whose Custom choice the owner just made; their exact rows are open. */
  custom: ReadonlySet<string>;
  /** Remote access through Portico Services: present only for a claimed, account-linked server. */
  secure?: boolean;
  /** Streaming: the capacity read that names the adapters and the conversion directory. */
  capacity?: TranscodeCapacity;
}>;

/** Values come from the drafts. Only the release-list fact is read from the configuration, with
 * the draft's address, because the Update checks choice is derived against its default. */
export function tvServerSettingsSources(state: TVServerSettingsState): SettingsBindingSources {
  const runtime = state.drafts.runtime, feed = state.configuration?.fields.find(field => field.id === 'general.updateFeed');
  return {
    configuration: state.configuration && feed && runtime ? {...state.configuration, fields: [{...feed, current: runtime['updates.feedUrl'], effective: runtime['updates.feedUrl']}]} : undefined,
    forms: Object.fromEntries(tvPageForms[state.page].map(id => [id, state.drafts[id]])),
  };
}

/** A row's bound value, or nothing when the page cannot change it here: no binding, another
 * page's document, a read-only fact, or a value only the config file sets. */
export function tvServerSettingFact(state: TVServerSettingsState, row: SettingsRow): SettingsBindingValue | undefined {
  const binding = settingsBinding(row.id);
  if (!binding || binding.readOnly || !tvPageForms[state.page].includes(binding.form as ServerFormId) || !state.configuration) return undefined;
  const policy = state.configuration.fields.find(field => field.id === policyId(row.id));
  if (policy?.configOnly || row.id === 'general.updateChecks' && typeof policy?.default !== 'string') return undefined;
  return readSettingsBinding(binding, tvServerSettingsSources(state), row.id);
}

export function tvServerSettingRead(state: TVServerSettingsState, id: string): unknown {
  if (state.custom.has(id)) return 'custom';
  if (id === 'remote.secure') return state.secure;
  const row = settingsRowIndex.get(id)?.row;
  return row ? tvServerSettingFact(state, row)?.current : undefined;
}

/** The TV page's rows in spec order. A dependent row is drawn only while its parent allows it
 * (TV Settings hides rather than disables), and folders, lists and actions stay off TV. */
export function tvServerSettingGroups(state: TVServerSettingsState, groups: readonly SettingsGroup[] | undefined): readonly TVSettingGroup[] {
  const read = (id: string) => tvServerSettingRead(state, id);
  const visit = (row: SettingsRow, dependent: boolean): TVSettingItem[] => {
    if (row.platforms && !row.platforms.includes('tv')) return [];
    if (row.enableWhen && !settingEnabled(row, read)) return [];
    const children = () => (row.children ?? []).flatMap(child => visit(child, true));
    if (row.id === 'remote.secure') return state.secure === undefined ? [] : [{id: row.id, row, dependent, kind: 'switch', checked: state.secure}, ...children()];
    if (row.id === 'streaming.transcodeLocation') {
      const fact = tvServerSettingFact(state, row);
      if (!fact) return [];
      const {value, memoryAvailable} = transcodeLocationChoice(state.capacity?.temporaryDirectory, typeof fact.current === 'string' ? fact.current : undefined);
      const options: TVChoiceModel[] = (row.control.options ?? []).map(option => ({value: String(option.value), label: option.label, hint: option.hint}));
      const folder = row.children?.find(child => child.id === 'streaming.transcodeFolder');
      const stored = state.drafts.runtime?.temporaryDirectory;
      return [{id: row.id, row: memoryAvailable ? row : {...row, help: 'next.settings.streaming.memoryUnavailable'}, dependent, kind: 'choice', value, options},
        ...(folder && typeof stored === 'string' ? [{id: folder.id, row: folder, dependent: true, kind: 'folder' as const, value: stored}] : [])];
    }
    const fact = tvServerSettingFact(state, row);
    if (!fact) return children();
    const control = row.control;
    const current = state.custom.has(row.id) ? 'custom' : row.id === 'streaming.maxHardwareConversions' && fact.current === 'unlimited' ? 'auto' : fact.current;
    let item: TVSettingItem | undefined;
    if (control.type === 'toggle' && typeof current === 'boolean') item = {id: row.id, row, dependent, kind: 'switch', checked: current};
    else if ((control.type === 'select' || control.type === 'segmented') && control.options) {
      const value = String(current), options: TVChoiceModel[] = row.id === 'streaming.hardware'
        // Automatic, Processor only, then each adapter the hardware report found.
        ? hardwareSelectionChoices(state.capacity?.hardware.devices ?? [], current, ((id: string) => id) as never).map(choice => ({value: choice.value, label: choice.label, disabled: choice.locked,
          plain: !(control.options ?? []).some(option => String(option.value) === choice.value) && choice.label !== 'next.settings.hardware.deviceGone'}))
        : control.options.map(option => ({value: String(option.value), label: option.label, hint: option.hint, disabled: option.locked}));
      // An exact server value is never rounded to the nearest named choice.
      if (!options.some(option => option.value === value)) options.push({value, label: value, disabled: true});
      item = {id: row.id, row, dependent, kind: 'choice', value, options};
    } else if (control.type === 'number' && (typeof current === 'number' || current === null)) item = {id: row.id, row, dependent, kind: 'number', value: current === null ? '' : String(current)};
    else if (control.type === 'text' && typeof current === 'string') item = control.secret ? {id: row.id, row, dependent, kind: 'secret', configured: current !== ''} : {id: row.id, row, dependent, kind: 'text', value: current};
    else if (control.type === 'matrix' && current && typeof current === 'object' && !Array.isArray(current)) {
      const values = current as Readonly<Record<string, unknown>>;
      item = {id: row.id, row, dependent, kind: 'matrix', values,
        items: (control.items ?? []).filter((entry): entry is typeof entry & {id: string} => !!entry.id && Object.hasOwn(values, entry.id)).map(entry => ({id: entry.id, label: entry.label, value: String(values[entry.id])})),
        columns: (control.columns ?? []).filter(column => column.value !== undefined).map(column => ({value: column.value!, label: column.label}))};
    }
    return item ? [item, ...children()] : children();
  };
  return (groups ?? []).filter(group => !group.platforms || group.platforms.includes('tv'))
    .map(group => ({title: group.title, items: group.rows.flatMap(row => visit(row, false))}))
    .filter(group => group.items.length > 0);
}

/** Custom is derived from the exact rows in Advanced. A custom window is a stored mode, and
 * custom removal starts from the current number of days; other rows store nothing yet. */
export function tvCustomChoiceValue(row: SettingsRow, drafts: TVServerSettingsState['drafts']): unknown {
  const binding = settingsBinding(row.id);
  if (binding?.codec === 'window-mode') return 'custom';
  if (binding?.codec === 'missing-rule') {
    const days = drafts.runtime?.['library.missingTitlesDays'];
    return {days: typeof days === 'number' && Number.isSafeInteger(days) && days >= 1 ? days : 30};
  }
  return undefined;
}
