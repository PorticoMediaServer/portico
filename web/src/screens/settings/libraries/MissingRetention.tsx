import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {SettingsRow, SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings/SettingsRows';
import {Button, RichSelect, TextField} from '../../../kit/controls';

/** Choosing Custom only opens a draft; explicit commit writes mode and days together. */
export function MissingRetention({row, state, days, isCurrent, onChange}: Readonly<{row: SettingsRow; state: SettingPresentation; days: number; isCurrent: () => boolean; onChange: (value: SettingsValue) => void}>) {
  const [editing, setEditing] = useState(false), [draft, setDraft] = useState(String(days));
  const mounted = useRef(false);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const disabled = state.disabled || state.saving || state.readOnly;
  const numeric = Number(draft), valid = /^\d+$/.test(draft) && Number.isSafeInteger(numeric) && numeric >= 1 && numeric <= 3650;
  const t = defaultI18n.t;
  return <div>
    <RichSelect aria-label={t(row.label)} value={editing ? 'custom' : String(state.value)} disabled={disabled} options={(row.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}))} onValueChange={value => {
      if (!mounted.current || disabled || !isCurrent()) return;
      if (value === 'custom') {setDraft(String(days)); setEditing(true);} else {setEditing(false); onChange(value);}
    }} />
    {editing && <div>
      <TextField aria-label={t('next.settingsSpec.server.libraries.advanced.groups.0.rows.0.label')} inputMode="numeric" value={draft} disabled={disabled} onChange={event => setDraft(event.currentTarget.value)} />
      <Button disabled={disabled || !valid} onClick={() => {if (mounted.current && !disabled && valid && isCurrent()) {setEditing(false); onChange({days: numeric});}}}>{t('next.settings.apply')}</Button>
      <Button disabled={disabled} onClick={() => setEditing(false)}>{t('next.settings.cancel')}</Button>
    </div>}
  </div>;
}
