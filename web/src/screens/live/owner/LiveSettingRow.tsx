import type {ReactNode} from 'react';
import {DefaultHint, SettingsRow} from '../../../kit/shell/SettingsSurfaces';

export function LiveSettingRow({label, help, value, defaultValue, reset, children}: {label: ReactNode; help?: ReactNode; value: unknown; defaultValue: unknown; reset: () => void; children: ReactNode}) {
  const changed = JSON.stringify(value) !== JSON.stringify(defaultValue);
  return <SettingsRow label={label} help={help || changed ? <>{help}{changed && <DefaultHint value="" effectiveSource="custom" variant="changed" onReset={reset}/>}</> : undefined}>{children}</SettingsRow>;
}
