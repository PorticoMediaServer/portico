/**
 * Small helpers the Server pages share for what the console reports: a code read as words, a tone
 * for a job or an alert, a duration and a time. The names of jobs, job states and alerts come from
 * the catalogue (`console-catalogue.ts`), in the viewer's language.
 */
import type {Alert, Job} from '../console.ts';

export const sentence = (v: string) => { const words = v.replace(/[_.-]+/g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2').trim().toLowerCase(); return words.charAt(0).toUpperCase() + words.slice(1); };

export const jobTone = (state: Job['state']): 'healthy' | 'warning' | 'danger' | 'neutral' | 'accent' => (state === 'running' ? 'accent' : state === 'succeeded' ? 'healthy' : state === 'failed' ? 'danger' : state === 'paused' || state === 'cancellation-requested' || state === 'reconciling' ? 'warning' : 'neutral');

export const alertTone = (a: Alert): 'danger' | 'warning' | 'accent' => (a.severity === 'critical' ? 'danger' : a.severity === 'warning' ? 'warning' : 'accent');

export function duration(seconds: number): string {
  const s = Math.round(seconds);
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}
export function when(ms: number): string {
  if (!ms) return '—';
  const d = new Date(ms);
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  return sameDay ? d.toLocaleTimeString([], {hour: 'numeric', minute: '2-digit'}) : d.toLocaleString([], {month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit'});
}
