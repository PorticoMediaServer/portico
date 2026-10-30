import {defaultI18n, type I18n} from '@i18n';
import type {Measurement} from '@core/console.ts';
import {healthFacts} from '@core/server-admin/dashboard.ts';
import type {FactPart} from '@core/presentation/kit-model/index.ts';

/** The drawn header order, using only measurements returned by the selected server. */
export function settingsServerHeader(health?: Measurement, storage?: Measurement, i18n: I18n = defaultI18n): Readonly<{health?: 'normal' | 'attention'; facts: readonly FactPart[]}> {
  const measured = healthFacts(health, storage), facts: FactPart[] = [];
  const available = (value: number | undefined): value is number => value !== undefined && Number.isFinite(value) && value >= 0;
  if (available(measured.uptimeSeconds)) {
    const seconds = measured.uptimeSeconds;
    const [unit, divisor] = seconds >= 86400 ? ['day', 86400] as const : seconds >= 3600 ? ['hour', 3600] as const : seconds >= 60 ? ['minute', 60] as const : ['second', 1] as const;
    const duration = i18n.number(Math.floor(seconds / divisor), {style: 'unit', unit, unitDisplay: 'long', maximumFractionDigits: 0});
    facts.push({id: 'uptime', role: 'value', text: i18n.t('settings.server.health.upFor', {duration})});
  }
  if (available(measured.freeBytes)) facts.push({id: 'free', role: 'value', text: i18n.t('settings.server.health.free', {bytes: i18n.bytes(measured.freeBytes)})});
  if (available(measured.databaseBytes)) facts.push({id: 'database', role: 'value', text: i18n.t('settings.server.health.database', {bytes: i18n.bytes(measured.databaseBytes)})});
  return {health: health?.facts.database ? measured.degraded ? 'attention' : 'normal' : undefined, facts};
}
