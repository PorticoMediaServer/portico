/** Local editor state; the caller supplies resolved display copy and real airing facts. */
export type RuleSettings = Readonly<{enabled: boolean; episodes: 'new' | 'all'; channel?: 'this' | 'any'; channelLocked?: boolean; keep: string; startEarly: number; endLate: number}>;
export type RuleAiring = Readonly<{id: string; time: string; day: string; channelId: string; channel?: string; episode?: string; title: string; isNew: boolean; scheduled?: boolean}>;
export type SeriesRule = Readonly<{id: string; title: string; glyph: string; tone?: 'documentary' | 'news' | 'sport'; channelId: string; channelName?: string; recorded?: number; upcomingCount?: number; matchingTonight?: number; actualCounts?: boolean; next: string; showNextChannel?: boolean; conflict?: boolean; settings: RuleSettings; airings?: readonly RuleAiring[]}>;
export type SeriesRulesView = Readonly<{sourceName: string; fromServer?: boolean; channelCount?: number; guideThrough?: string; upcomingCount?: number; recordedCount?: number; ruleCount?: number; conflictCount?: number; storage?: Readonly<{used: number; free: number; hoursLeft: number}>; rules: readonly SeriesRule[]; selectedRuleId?: string}>;
export type RulesState = Readonly<{rules: readonly SeriesRule[]; selectedId?: string; draft?: RuleSettings}>;
export type RulesAction = Readonly<{type: 'select'; id: string}> | Readonly<{type: 'change'; patch: Partial<RuleSettings>}> | Readonly<{type: 'replace'; view: SeriesRulesView}> | Readonly<{type: 'cancel' | 'save' | 'delete'}>;
export function matchingAirings(rule: SeriesRule, settings: RuleSettings = rule.settings): readonly RuleAiring[] {
  return settings.enabled ? (rule.airings ?? []).filter(airing => (settings.episodes === 'all' || airing.isNew) && (settings.channel === 'any' || airing.channelId === rule.channelId)) : [];
}
export function matchingCount(rule: SeriesRule, settings: RuleSettings = rule.settings): number | undefined {
  return settings.enabled ? rule.actualCounts ? rule.upcomingCount : rule.airings ? matchingAirings(rule, settings).length : rule.upcomingCount : 0;
}
export function createRulesState(view: SeriesRulesView): RulesState {
  const rule = view.rules.find(rule => rule.id === view.selectedRuleId);
  return {rules: view.rules, ...(rule ? {selectedId: rule.id, draft: {...rule.settings}} : {})};
}
export function rulesReducer(state: RulesState, action: RulesAction): RulesState {
  if (action.type === 'replace') {
    const selected = state.rules.find(rule => rule.id === state.selectedId);
    return selected && action.view.rules.includes(selected) ? {...state, rules: action.view.rules} : createRulesState(action.view);
  }
  if (action.type === 'select') {
    const rule = state.rules.find(rule => rule.id === action.id);
    return rule ? {...state, selectedId: rule.id, draft: {...rule.settings}} : state;
  }
  if (action.type === 'change') return state.draft ? {...state, draft: {...state.draft, ...action.patch}} : state;
  if (action.type === 'cancel') return {rules: state.rules};
  if (action.type === 'delete') return {rules: state.rules.filter(rule => rule.id !== state.selectedId)};
  return {rules: state.rules.map(rule => rule.id === state.selectedId && state.draft ? {...rule, settings: {...state.draft}} : rule)};
}
