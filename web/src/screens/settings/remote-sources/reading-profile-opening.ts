import type {RemoteSource, RemoteSourceService, SourceAdvanced, SourceAdvancedSettings, SourceChoiceApplyResult, SourceReadSettings, SourceReadValues} from '@core/remote-sources.ts';
import {sourceChoiceCommand, sourceChoiceComparison, sourceChoiceFields, sourceChoiceResetDefinition, type SourceChoiceDefinition, type SourceChoiceField} from '@core/remote-source-choice.ts';

type ReadingService = Pick<RemoteSourceService, 'readProfile' | 'readAdvanced' | 'saveProfile' | 'saveAdvanced' | 'saveProfileStartChoice' | 'recoverStartChoice'>;
type AdvancedField = Exclude<SourceChoiceField, 'scanDepth' | 'connections' | 'requestsPerSecond' | 'bytesPerSecond'>;
const advancedFields = sourceChoiceFields.filter((field): field is AdvancedField => !['scanDepth', 'connections', 'requestsPerSecond', 'bytesPerSecond'].includes(field));
export type ReadingDraft = Readonly<{
  profile: SourceReadSettings; advanced?: SourceAdvancedSettings; values: SourceReadValues;
  consent?: boolean; consentDirty: boolean; choice?: SourceChoiceDefinition;
  advancedValues?: SourceAdvanced; advancedDirty?: boolean;
}>;
export type ReadingOpeningState =
  | Readonly<{kind: 'loading' | 'retired'}>
  | Readonly<{kind: 'ready'; draft: ReadingDraft}>
  | Readonly<{kind: 'saving'; draft: ReadingDraft}>
  | Readonly<{kind: 'failed'; draft?: ReadingDraft; error: unknown}>
  | Readonly<{kind: 'unknown'; draft: ReadingDraft; result: Extract<SourceChoiceApplyResult, {kind: 'unknown'}>}>
  | Readonly<{kind: 'accepted-unavailable'; result: Extract<SourceChoiceApplyResult, {kind: 'accepted-unavailable'}>}>
  | Readonly<{kind: 'saved-unavailable'}>
  | Readonly<{kind: 'saved'; profile?: SourceReadSettings}>;

/** One committed Reading dialog. The source, original paired reads, draft and
 * command never migrate to another opening. Recovery uses only the SDK-issued
 * intent; it neither reads a replacement pair nor resends a write. */
export class ReadingProfileOpening {
  private live = true;
  private started = false;
  private flight?: object;
  private command?: object;
  private readonly abort = new AbortController();
  private state: ReadingOpeningState = Object.freeze({kind: 'loading'});
  private readonly listeners = new Set<() => void>();
  constructor(private readonly service: ReadingService, readonly source: RemoteSource, private readonly isCurrent: () => boolean,
    private readonly canWrite: () => boolean) {}
  current = () => {try {return this.live && !this.abort.signal.aborted && this.isCurrent();} catch {return false;}};
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: ReadingOpeningState) {
    if (!this.current()) return;
    this.state = Object.freeze(state);
    for (const listener of this.listeners) {try {listener();} catch { /* A view listener cannot change an operation's outcome. */ }}
  }
  load = async () => {
    if (this.started || !this.current()) return;
    this.started = true;
    try {
      // A failed optional Advanced read withholds atomic choices, while the
      // existing independent Reading/consent editor can still use its own read.
      const [profile, advanced] = await Promise.all([
        this.service.readProfile(this.source.id, String(this.source.generation), this.current),
        this.service.readAdvanced(this.source.id, this.current).catch(() => undefined),
      ]);
      if (!this.current()) return;
      if (profile.sourceId !== this.source.id || profile.generation !== String(this.source.generation)) return;
      const paired = advanced?.sourceId === profile.sourceId && advanced.generation === profile.generation ? advanced : undefined;
      const {allowAdvancedMediaAnalysis: consent, ...values} = profile.values;
      this.publish({kind: 'ready', draft: Object.freeze({profile, advanced: paired, advancedValues: paired?.values, values: Object.freeze(values), consent, consentDirty: false})});
    } catch (error) {this.publish({kind: 'failed', error});}
  };
  private holds(draft: ReadingDraft) {
    return this.current() && !this.flight && (this.state.kind === 'ready' || this.state.kind === 'failed') && this.state.draft === draft;
  }
  offers = (draft: ReadingDraft) => {
    const definitions = draft.profile.choiceDefinitions, advanced = draft.advanced;
    return definitions && advanced && definitions.every(definition => sourceChoiceCommand(draft.profile, advanced, definition)) ? definitions : undefined;
  };
  recorded = (draft: ReadingDraft) => {
    const {profile, advanced, values} = draft, baseline = profile.choiceBaseline;
    const comparison = sourceChoiceComparison(profile), definition = sourceChoiceResetDefinition(profile);
    if (draft.choice || !baseline || !comparison || !definition || !advanced || advanced.revision !== baseline.advancedRevision ||
      advanced.sourceId !== profile.sourceId || advanced.generation !== profile.generation ||
      values.scanDepth !== profile.values.scanDepth || values.connections !== profile.values.connections ||
      values.requestsPerSecond !== profile.values.requestsPerSecond || values.bytesPerSecond !== profile.values.bytesPerSecond ||
      draft.advancedDirty) return undefined;
    // The independent Advanced GET must agree with this read's own baseline,
    // not a newer revision or merely the same named choice.
    if (!sourceChoiceFields.every(field => {
      switch (field) {
        case 'scanDepth': case 'connections': case 'requestsPerSecond': case 'bytesPerSecond': return true;
        default: return advanced.values[field] === baseline.currentAdvanced[field];
      }
    })) return undefined;
    return {comparison, definition};
  };
  select = (draft: ReadingDraft, choice: SourceChoiceDefinition) => {
    if (!this.holds(draft) || draft.consentDirty || !this.offers(draft)?.includes(choice)) return;
    this.publish({kind: 'ready', draft: Object.freeze({...draft, choice, advancedDirty: false, values: Object.freeze({
      ...choice.reading, wholeFileReads: draft.profile.values.wholeFileReads,
    })})});
  };
  edit = (draft: ReadingDraft, patch: Partial<Pick<SourceReadValues, 'connections' | 'requestsPerSecond' | 'bytesPerSecond'>>) => {
    if (!this.holds(draft) || draft.choice || draft.advancedDirty) return;
    this.publish({kind: 'ready', draft: Object.freeze({...draft, values: Object.freeze({...draft.values, ...patch})})});
  };
  changeConsent = (draft: ReadingDraft, consent: boolean) => {
    if (!this.holds(draft) || draft.choice || draft.advancedDirty || draft.consent === undefined) return;
    this.publish({kind: 'ready', draft: Object.freeze({...draft, consent, consentDirty: true})});
  };
  previewAdvanced = (draft: ReadingDraft) => {
    if (!draft.choice || !draft.advanced) return draft.advancedValues;
    const values = {...draft.advanced.values}, applicable = draft.profile.choiceApplicableFields ?? [];
    for (const field of advancedFields) if (applicable.includes(field)) Object.assign(values, {[field]: draft.choice.advanced[field]});
    return values;
  };
  private readingUnchanged(draft: ReadingDraft) {
    return draft.values.scanDepth === draft.profile.values.scanDepth && draft.values.connections === draft.profile.values.connections &&
      draft.values.requestsPerSecond === draft.profile.values.requestsPerSecond && draft.values.bytesPerSecond === draft.profile.values.bytesPerSecond;
  }
  editAdvanced = (draft: ReadingDraft, values: SourceAdvanced) => {
    if (!this.holds(draft) || !draft.advanced || draft.choice || draft.consentDirty || !this.readingUnchanged(draft)) return;
    const applicable = draft.profile.choiceApplicableFields;
    if (applicable && advancedFields.some(field => !applicable.includes(field) && values[field] !== draft.advanced!.values[field])) return;
    this.publish({kind: 'ready', draft: Object.freeze({...draft, advancedValues: Object.freeze({...values}), advancedDirty: true})});
  };
  resetField = (draft: ReadingDraft, rendered: ReadingOpeningState, field: SourceChoiceField) => {
    if (this.state !== rendered || !this.holds(draft) || draft.consentDirty) return;
    const recorded = this.recorded(draft);
    if (!recorded || !recorded.comparison.applicableFields.includes(field) || !recorded.comparison.changedFields.includes(field)) return;
    const definition = recorded.definition;
    if (field === 'connections' || field === 'requestsPerSecond' || field === 'bytesPerSecond') this.edit(draft, {[field]: definition.reading[field]});
    else if (field !== 'scanDepth' && draft.advancedValues) this.editAdvanced(draft, {...draft.advancedValues, [field]: definition.advanced[field]});
  };
  saveAdvanced = async (draft: ReadingDraft, rendered: ReadingOpeningState) => {
    if (this.state !== rendered || !this.holds(draft) || !draft.advanced || !draft.advancedValues || !draft.advancedDirty ||
      draft.choice || draft.consentDirty || !this.readingUnchanged(draft)) return;
    const flight = {};this.flight = flight;
    const current = () => this.current() && this.flight === flight;
    try {
      if (!this.current() || !this.canWrite()) return;
      this.publish({kind: 'saving', draft});
      await this.service.saveAdvanced(draft.advanced, draft.advancedValues, current);
      if (current()) this.publish({kind: 'saved'});
    } catch (error) {if (current()) this.publish({kind: 'failed', draft, error});}
    finally {if (this.flight === flight) this.flight = undefined;}
  };
  save = async (draft: ReadingDraft, rendered: ReadingOpeningState) => {
    if (this.state !== rendered || this.flight || draft.advancedDirty || (this.state.kind !== 'ready' && this.state.kind !== 'failed') || this.state.draft !== draft) return;
    // Claim before caller predicates, publication or SDK callbacks can re-enter.
    const flight = {};this.flight = flight;
    const current = () => this.current() && (draft.choice ? this.command === flight : this.flight === flight);
    try {
      if (!this.current() || !this.canWrite()) return;
      if (draft.choice) this.command = flight;
      this.publish({kind: 'saving', draft});
      if (draft.choice) {
        if (!draft.advanced || draft.consentDirty) return;
        const result = await this.service.saveProfileStartChoice(draft.profile, draft.advanced, draft.choice,
          Object.freeze({signal: this.abort.signal, isCurrent: current, consentDirty: false}));
        if (current()) this.settle(draft, result);
      } else {
        const values = draft.consentDirty && draft.consent !== undefined ? {...draft.values, allowAdvancedMediaAnalysis: draft.consent} : draft.values;
        const profile = await this.service.saveProfile(draft.profile, values, current);
        if (current()) this.publish({kind: 'saved', profile});
      }
    } catch (error) {
      if (current()) {
        // The ordinary save API also distinguishes a known save whose refresh
        // failed. Keep its existing close/no-repeat semantics, not another PUT.
        const refreshFailed = !draft.choice && typeof error === 'object' && error !== null && 'code' in error && error.code === 'refresh_failed';
        this.publish(refreshFailed ? {kind: 'saved-unavailable'} : {kind: 'failed', draft, error});
      }
    }
    finally {if (this.flight === flight) this.flight = undefined;}
  };
  private settle(draft: ReadingDraft, result: SourceChoiceApplyResult) {
    if (result.kind === 'applied') this.publish({kind: 'saved', profile: result.settings});
    else if (result.kind === 'accepted-unavailable') this.publish({kind: 'accepted-unavailable', result});
    else this.publish({kind: 'unknown', draft, result});
  }
  recover = async (expected: Extract<ReadingOpeningState, {kind: 'unknown'}>) => {
    if (this.flight || this.state !== expected) return;
    const flight = {};this.flight = flight;
    try {
      if (!this.current()) return;
      this.publish({kind: 'saving', draft: expected.draft});
      const result = await this.service.recoverStartChoice(expected.result.intent);
      if (this.current() && this.flight === flight) this.settle(expected.draft, result);
    } catch (error) {
      if (this.current() && this.flight === flight) this.publish({kind: 'unknown', draft: expected.draft,
        result: Object.freeze({...expected.result, error})});
    } finally {if (this.flight === flight) this.flight = undefined;}
  };
  retire = () => {
    if (!this.live) return;
    this.live = false;this.abort.abort();this.state = Object.freeze({kind: 'retired'});
    for (const listener of this.listeners) {try {listener();} catch { /* Retire every subscriber independently. */ }}
    this.listeners.clear();
  };
}
