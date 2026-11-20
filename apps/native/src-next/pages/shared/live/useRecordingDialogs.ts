import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type { View } from 'react-native';
import {type Recording, type RecordingDetail, type RecordingRule, type RuleConfig} from '../../../../../../packages/client-core/src/dvr';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {RecordingOverlapWindow, type OverlapSnapshot} from '../../../screens/settings-live/recording-overlap-window';
import {ruleFacts} from '../../../screens/settings-live/recording-rule-presentation';
import type { NativeRecordingsController } from '../../../screens/settings-live/recordings-controller';
import type { NativeShellRuntime } from '../../../shell/runtime';

import type { OverlayOpening } from './useRecordings';

/** One opening of the details sheet. The parent keys the sheet by its row and this
 * opening retires itself as well, so either side alone stops a stale completion. The opening
 * also holds the recording and the currency callback it was drawn for, so its read can only
 * ever use the witnesses of the render that committed it. */
type RecordingOpening = {id: string; revision: number; controller: NativeRecordingsController; parent: OverlayOpening; recording: Recording; isCurrent: () => boolean; live: boolean};

export type RecordingDetailsProps = {recording: Recording; controller: NativeRecordingsController; runtime: NativeShellRuntime; parent: OverlayOpening; isCurrent: () => boolean; opener: React.RefObject<View | null>; onClose: () => void; onPlay: () => void; onRemove: () => void; deletionAvailable: boolean};
export type RuleEditorProps = {rule: RecordingRule; controller: NativeRecordingsController; runtime: NativeShellRuntime; isCurrent: () => boolean; opener: React.RefObject<View | null>; onClose: () => void};

export function useRecordingDetails({recording, controller, runtime, onClose, isCurrent, parent}: RecordingDetailsProps) {
  const t = defaultI18n.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const conflict = recording.state === 'conflicted';
  const primary = useRef<View>(null);
  // Retire the previous opening during render, before the replacement draws, so a
  // completion or a read that belongs to A can never act on B.
  const opening = useRef<RecordingOpening | undefined>(undefined);
  const previous = opening.current;
  const opened: RecordingOpening = previous && previous.id === recording.id && previous.revision === recording.revision && previous.controller === controller && previous.parent === parent
    ? previous : {id: recording.id, revision: recording.revision, controller, parent, recording, isCurrent, live: true};
  if (previous && previous !== opened) previous.live = false;
  opening.current = opened;
  // The opening this component committed, written only by the layout pass below. The passive
  // read copies it once and every settlement retains that copy, so a render React throws away
  // can never hand its witnesses to a read.
  const committed = useRef<RecordingOpening | undefined>(undefined);
  // A layout effect retires the opening on unmount as well, before any later callback
  // can land; a passive cleanup would leave it live through a replacement. It is keyed by the
  // same values that decide the opening, and it is where the read takes its witnesses from.
  useLayoutEffect(() => {
    const drawn = opening.current; if (!drawn) return;
    committed.current = drawn; drawn.live = true;
    return () => {drawn.live = false; if (committed.current === drawn) committed.current = undefined;};
  }, [opened.id, opened.revision, opened.controller, opened.parent]);
  // The read carries the opening it was made for, so A's state, capacity and overlaps
  // are never drawn under B's title. The recording and the currency callback travel on that
  // committed opening, so they are exactly the values of the render that committed it.
  const [read, setRead] = useState<Readonly<{for: RecordingOpening; detail?: RecordingDetail; resolver?: ReturnType<NativeRecordingsController['detailOpening']>; error?: unknown}>>();
  const [overlap, setOverlap] = useState<Readonly<{for: RecordingOpening; window: RecordingOverlapWindow; snapshot: OverlapSnapshot}>>();
  useEffect(() => {
    const drawn = committed.current;
    if (!drawn) return;
    let active = true, window: RecordingOverlapWindow | undefined;
    const resolver = drawn.controller.detailOpening(drawn.recording, () => active && drawn.live && drawn.isCurrent());
    if (drawn.isCurrent()) void resolver.read().then(value => {
      if (!active || !drawn.live || !drawn.isCurrent()) return;
      try {
        window = new RecordingOverlapWindow(drawn.recording, value, drawn.controller, () => active && drawn.live && drawn.isCurrent(), snapshot => {
          if (active && drawn.live && window && drawn.isCurrent()) setOverlap({for: drawn, window, snapshot});
        });
        setRead({for: drawn, detail: value, resolver}); window.seed(value);
      } catch (error) {if (active && drawn.live && drawn.isCurrent()) setRead({for: drawn, error});}
    }, value => {
      if (active && drawn.live && drawn.isCurrent()) setRead({for: drawn, error: value});
    });
    return () => {active = false; resolver.dispose(); window?.dispose();};
  }, [opened.id, opened.revision, opened.controller, opened.parent]);
  const current = read?.for === opened ? read : undefined;
  const detail = current?.detail, error = current?.error;
  const overlapCurrent = overlap?.for === opened ? overlap : undefined;
  const runResolve = (resolver: NonNullable<typeof current>['resolver'], value: RecordingDetail) => {
    if (!resolver) return;
    void resolver.resolve(value).then(result => {if (result && opened.live && isCurrent()) onClose();}, error => {if (opened.live && isCurrent()) runtime.reportError(error);});
  };
  return {t, state, conflict, primary, opened, current, detail, error, overlapCurrent, runResolve};
}

export function useRecordingRuleEditor({rule, controller, runtime, onClose, isCurrent}: RuleEditorProps) {
  const t = runtime.text.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [config, setConfig] = useState<RuleConfig>(() => ({...rule.config, options: {...rule.config.options}})), [deleting, setDeleting] = useState(false), [future, setFuture] = useState<'keep' | 'cancel'>('keep');
  const save = useRef<View>(null), cancel = useRef<View>(null), keep = useRef<View>(null), facts = ruleFacts(rule.summary);
  const mounted = useRef(false);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, [controller, rule.id, rule.revision]);
  const current = () => mounted.current && isCurrent() && controller.lifetime.current() && runtime.getSnapshot().destination.kind === 'live' && runtime.getSnapshot().contentVisible !== false;
  const close = () => {if (current() && !controller.getSnapshot().busy) onClose();};
  const offsets = [0, 60, 120, 300, 600, 900], minutes = (value: number) => t(value ? 'recordings.minutes' : 'recordings.none', {count: value / 60});
  const write = async (next: RuleConfig) => {if (!current() || controller.getSnapshot().busy) return; const result = await controller.saveRule(rule, next); if (result && current()) onClose();};
  const remove = async () => {if (!current() || controller.getSnapshot().busy) return; const result = await controller.deleteRule(rule, future); if (result && current()) onClose();};
  return {t, state, config, setConfig, deleting, setDeleting, future, setFuture, save, cancel, keep, facts, current, close, offsets, minutes, write, remove};
}
