import {defaultI18n} from '@i18n';
import {programmeFactParts, type ProgrammeFacts} from '@core/presentation/kit-model/channels';
import {LiveCard} from '../../src/kit/cards/FeatureCards';

// Supplied data, projected by the same canonical helper as Home. The fixed clock
// keeps before/after renders identical without an API or a live guide refresh.
const startMs = Date.parse('2026-10-22T13:30:00Z');
const nowMs = startMs + 39 * 60_000;
const env = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
export const programmeInputs: Readonly<Record<'home' | 'rich', ProgrammeFacts>> = {
  home: {startMs, endMs: startMs + 60 * 60_000, categories: ['News']},
  rich: {startMs, endMs: startMs + 60 * 60_000, subtitle: 'An extended report from the international news desk', categories: ['World news and current affairs'], rating: 'TV-PG'},
};
export type FactsCase = keyof typeof programmeInputs;
export const factsFor = (kind: FactsCase) => programmeFactParts(programmeInputs[kind], env, nowMs);
export const channel = {sourceId: 'supplied-news', id: 'northwind-news', name: 'Northwind News', number: '101', mark: {sourceId: 'supplied-news', id: 'northwind-news', text: 'NN', seed: 'supplied-news:northwind-news'}};
export function SuppliedLiveFacts({kind}: {kind: FactsCase}) {
  return <LiveCard channel={channel} programme={{title: 'Morning Briefing', facts: factsFor(kind), progress: .65}} onNavigate={() => {}} />;
}
export const suppliedLiveElement = (kind: FactsCase) => <SuppliedLiveFacts kind={kind} />;

// Exact caller-supplied content from Cards r7 root-review-index 007-LiveCard.
export const suppliedChannelElement = () => <LiveCard
  channel={{sourceId: 'drawn-source', id: 'drawn-channel', name: 'Northwind News', number: '101', mark: {sourceId: 'drawn-source', id: 'drawn-channel', text: 'NN', seed: 'drawn-source:drawn-channel'}}}
  programme={{title: 'World Tonight', facts: [{id: '0', role: 'supplement', text: 'Ends 4:30 PM'}], progress: .58, live: true, extra: []}}
  tint="#7a3b3b" onNavigate={() => {}}
/>;
