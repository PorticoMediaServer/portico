import {
  formatSignalText, musicSignal, musicSignalAccessibleName,
  type MusicSignalInput, type MusicSignalModel, type SignalStep, type SignalText, type SignalTranslate,
} from '../../../../../../packages/client-core/src/music-signal';

export type NativeSignalStep = Readonly<{
  id: SignalStep['id']; label: string; value?: string; note?: string; percent?: number; changed: boolean;
}>;
export type NativeSignalPath = Readonly<{
  source?: string; state?: string; stateLoss?: string; accessibleName?: string;
  pill: string; rule: MusicSignalModel['rule']; healthy: boolean; steps: readonly NativeSignalStep[];
}>;

/** Presentation only. The host supplies measured current-session facts: requested
 * EQ, output capabilities and platform names are never proof of applied effects.
 * Rule precedence and technical claims remain owned by the shared signal model. */
export function nativeSignalPath(input: MusicSignalInput, t: SignalTranslate): NativeSignalPath {
  const model = musicSignal(input);
  const resolve = (value: SignalText | string | undefined) => typeof value === 'string' ? value : value ? formatSignalText(value, t) : undefined;
  return Object.freeze({
    source: resolve(model.shortSource), state: resolve(model.state), stateLoss: resolve(model.stateLoss),
    accessibleName: musicSignalAccessibleName(model, t), pill: formatSignalText(model.pill, t),
    rule: model.rule, healthy: model.healthy,
    steps: Object.freeze(model.steps.map(step => Object.freeze({
      id: step.id, label: resolve(step.label)!, value: resolve(step.value), note: resolve(step.note),
      ...(step.percent === undefined ? {} : {percent: step.percent}), changed: step.changed,
    }))),
  });
}
