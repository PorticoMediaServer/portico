import {Fragment, type HTMLAttributes, type ReactNode, type SyntheticEvent} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {SettingsGroup as GroupSpec, SettingsHolder, SettingsOption, SettingsRow as RowSpec, SettingsValue} from '@core/presentation/settings-spec.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {browserSettingsEnvironment, settingsRowVisible, type SettingsEnvironment} from '@core/presentation/settings-visibility.ts';
import {ChoiceMatrix, MatchText, SettingsRow} from '../shell/SettingsSurfaces';
import {Button, RichSelect, Segmented, Switch, TextField} from '../controls';
import {SettingNumber} from './SettingNumber';
import {SettingText} from './SettingText';
import {SettingsAdvanced, SettingsGroup} from './SettingsGroups';
import {SettingsGrid} from './SettingsGrid';
import {settingsConditionMet} from './condition';
import type {SettingsTableColumn, SettingsTableRow} from './SettingsTable';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type SettingPresentation = Readonly<{
  present: boolean; control?: ReactNode; value?: SettingsValue; defaultValue?: SettingsValue; changed?: boolean; displayValue?: string; defaultLabel?: string;
  disabled?: boolean; saving?: boolean; readOnly?: boolean; error?: string; statusTone?: 'quiet' | 'ok' | 'warn' | 'bad';
  /** Static copy is translated once; dynamic names are genuine producer values. */
  choices?: readonly (Omit<SettingsOption, 'label' | 'hint'> & {label: string; hint?: string})[]; label?: string; help?: string;
  gridRows?: readonly SettingsTableRow[];
}>;
export type SettingsRowsProps = Readonly<{
  holder: SettingsHolder; state: (row: RowSpec) => SettingPresentation; change: (row: RowSpec, value: SettingsValue) => void; reset: (row: RowSpec) => void;
  renderSpecial: (row: RowSpec, state: SettingPresentation) => ReactNode; query?: string; foundRowId?: string;
  advancedOpen: boolean; onAdvancedChange: (open: boolean) => void;
  environment?: SettingsEnvironment;
}>;
function children(rows: readonly RowSpec[]): readonly RowSpec[] {return rows.flatMap(row => [row, ...children(row.children ?? [])]);}

/** Controlled approved structure. It never invents runtime values or successful mutations. */
export function SettingsRows(input: SettingsRowsProps) {
  const environment = input.environment ?? browserSettingsEnvironment;
  const props = {...input, state: (row: RowSpec): SettingPresentation => settingsRowVisible(row, environment) ? input.state(row) : {present: false}};
  const advanced = (props.holder.advanced?.groups ?? []).flatMap(group => children(group.rows)).filter(row => props.state(row).present);
  const summary = advanced.filter(row => props.state(row).changed).map(row => `${props.state(row).label ?? t(row.label)}: ${props.state(row).displayValue ?? ''}`);
  return <>
    {(props.holder.groups ?? []).map((group, index) => <Group key={index} group={group} {...props} />)}
    {advanced.length > 0 && <SettingsAdvanced open={props.advancedOpen} onOpenChange={props.onAdvancedChange} summary={summary}>{props.holder.advanced?.groups?.map((group, index) => <Group key={index} group={group} advanced {...props} />)}</SettingsAdvanced>}
  </>;
}
function Group({group, advanced, ...props}: SettingsRowsProps & {group: GroupSpec; advanced?: boolean}) {
  const rows = group.rows.filter(row => props.state(row).present);
  if (!rows.length) return null;
  const flat = rows.length === 1 && ['table', 'list'].includes(rows[0].control.type) && !rows[0].control.reorder;
  return <SettingsGroup title={group.title && t(group.title)} description={group.description && t(group.description)} advanced={advanced} plain={flat}>
    {rows.map(row => <Row key={row.id} row={row} {...props} flat={flat} />)}
  </SettingsGroup>;
}
function Row({row, flat, dependent = false, inheritedDisabled = false, ...props}: SettingsRowsProps & {row: RowSpec; flat?: boolean; dependent?: boolean; inheritedDisabled?: boolean}) {
  const state = props.state(row);
  if (!state.present) return null;
  const parent = row.enableWhen && settingsRowIndex.get(row.enableWhen.id)?.row;
  const condition = parent && props.state(parent).value;
  const conditionMet = settingsConditionMet(row.enableWhen, condition);
  const disabled = inheritedDisabled || state.disabled || state.readOnly || state.saving || !conditionMet;
  // Keep the values in the accessibility tree. Custom controls still inherit
  // the row's interaction guard, including a disabled parent or an active save.
  const guard = (event: SyntheticEvent) => {if (disabled) {event.preventDefault(); event.stopPropagation();}};
  const controlState: HTMLAttributes<HTMLDivElement> = {'aria-disabled': disabled || undefined, 'aria-busy': state.saving || undefined,
    onClickCapture: guard, onChangeCapture: guard, onKeyDownCapture: event => {if (event.key !== 'Tab' && event.key !== 'Escape') guard(event);}};
  const c = row.control, label = state.label ?? t(row.label), options = state.choices ?? c.options?.map(option => ({...option, label: t(option.label), hint: option.hint && t(option.hint)})) ?? [];
  const changed = state.changed && !row.noChanged;
  const defaultLabel = state.defaultLabel ?? (options.find(option => option.value === state.defaultValue)?.label ?? (typeof state.defaultValue === 'boolean' ? t(state.defaultValue ? 'next.settings.on' : 'next.settings.off') : state.defaultValue === null ? t('next.settings.notSet') : state.defaultValue === undefined ? undefined : String(state.defaultValue)));
  const reset = changed && <span className={styles.changed}>{t('next.shell.changed')}<button type="button" disabled={disabled} onClick={() => {if (!disabled) props.reset(row);}}>{t('next.shell.reset')}</button></span>;
  let control: ReactNode;
  const change = (value: SettingsValue) => {if (!disabled) props.change(row, value);};
  const option = (text: string) => options.find(option => String(option.value) === text)?.value;
  if (state.control !== undefined) control = state.control;
  else if (state.readOnly && (state.displayValue !== undefined || state.value !== undefined)) control = <span className={c.mono ? styles.readonlyMono : styles.readonly}>{state.displayValue ?? String(state.value)}</span>;
  else if (c.type === 'toggle' && typeof state.value === 'boolean') control = <Switch aria-label={label} checked={state.value} disabled={disabled} onCheckedChange={change} />;
  else if (c.type === 'select' && state.value !== undefined) {
    const preset = options.some(option => option.sets);
    const locked = options.find(option => option.value === state.value)?.locked;
    control = locked ? <span className={styles.readonly}>{options.find(option => option.value === state.value)?.label}</span> : <RichSelect className={styles.settingSelect} aria-label={label} disabled={disabled} value={String(state.value)} options={options.filter(option => !option.locked).map(option => ({value: String(option.value), label: option.label, description: option.hint, isDefault: option.value === state.defaultValue, disabled: preset && !option.sets}))} onValueChange={value => {const next = option(value); if (next !== undefined) change(next);}} />;
  } else if (c.type === 'segmented' && state.value !== undefined) control = <Segmented aria-label={label} value={String(state.value)} disabled={disabled} items={options.map(option => ({value: String(option.value), label: option.label, disabled: options.some(value => value.sets) && !option.sets}))} onValueChange={value => {const next = option(value); if (next !== undefined) change(next);}} />;
  else if (c.type === 'number' && (typeof state.value === 'number' || state.value === null)) control = <SettingNumber label={label} value={state.value} min={c.min ?? 0} max={c.max ?? Number.MAX_SAFE_INTEGER} step={c.step} optional={c.optional} unit={c.unit && t(c.unit)} placeholder={c.placeholder && t(c.placeholder)} disabled={disabled} plain={c.style === 'field'} onCommit={change} />;
  else if (c.type === 'text' && !c.secret && typeof state.value === 'string') control = <SettingText label={label} value={state.value} disabled={disabled} placeholder={c.placeholder && t(c.placeholder)} onCommit={change} />;
  else if (c.type === 'status' && state.displayValue !== undefined) control = <span className={styles.status} data-tone={state.statusTone ?? 'quiet'}>{state.displayValue}</span>;
  else if (c.type === 'readonly' && (state.displayValue !== undefined || state.value !== undefined)) control = <span className={c.mono ? styles.readonlyMono : styles.readonly}>{state.displayValue ?? String(state.value)}</span>;
  else if (c.type === 'table' && c.style === 'grid' && state.gridRows) control = <SettingsGrid label={label} rows={state.gridRows} columns={(c.columns ?? []).map(column => ({id: column.id!, label: t(column.label), kind: column.kind as SettingsTableColumn['kind'], width: column.width}))} />;
  else if (c.type === 'matrix' && state.value && typeof state.value === 'object' && !Array.isArray(state.value)) {
    const values = state.value as Readonly<Record<string, SettingsValue>>, defaults = state.defaultValue as Readonly<Record<string, SettingsValue>> | undefined;
    control = <div className={styles.matrixControl} {...controlState}><ChoiceMatrix label={label} disabled={disabled} choices={(c.columns ?? []).map(column => ({value: column.value!, label: t(column.label), description: column.hint && t(column.hint)}))}
      rows={(c.items ?? []).map(item => ({id: item.id!, label: t(item.label), value: String(values[item.id!]), hint: defaults && values[item.id!] !== defaults[item.id!] ? <span className={styles.changed}>{t('next.shell.changed')}<button type="button" disabled={disabled} onClick={() => change({...values, [item.id!]: defaults[item.id!]})}>{t('next.shell.reset')}</button></span> : undefined}))} onChange={(id, value) => change({...values, [id]: value})} /></div>;
  } else control = props.renderSpecial(row, {...state, disabled});
  const body = flat || c.type === 'matrix' ? <div id={row.id} className={styles.specialRow} data-found={props.foundRowId === row.id || undefined} {...controlState}>{control}</div> : <SettingsRow id={row.id} dependent={dependent} found={props.foundRowId === row.id} stacked={['preview', 'list', 'table'].includes(c.type)}
    label={<><MatchText text={label} query={props.query ?? ''} />{reset}</>} help={state.help ?? (row.help && <MatchText text={t(row.help)} query={props.query ?? ''} />)} className={disabled ? styles.disabled : undefined}>
    <div className={styles.control} {...controlState}>{changed && defaultLabel !== undefined && ['toggle','select','segmented','number'].includes(c.type) && <span className={styles.defaultValue}>{t('next.settings.defaultValue', {value: defaultLabel})}</span>}{control}</div>
  </SettingsRow>;
  return <Fragment>{body}{state.error && <p className={styles.rowError} role="alert">{state.error}</p>}{row.children?.map(child => <Row key={child.id} row={child} {...props} dependent inheritedDisabled={disabled} />)}</Fragment>;
}
