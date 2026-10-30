import type {SettingsRow, SettingsValue} from '@core/presentation/settings-spec.ts';

/** Both clauses constrain the actual typed parent value. Missing producer
 * values cannot satisfy an allow-list by string coercion. */
export function settingsConditionMet(condition: SettingsRow['enableWhen'], value: SettingsValue | undefined): boolean {
  return (!condition?.in || condition.in.some(expected => expected === value))
    && (!condition?.notIn || !condition.notIn.some(expected => expected === value));
}
