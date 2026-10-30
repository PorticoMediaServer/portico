import type {ReadingPreferences} from '@portico/client-core';
import {readerErrorCode, type ReadingController, type ReaderErrorCode} from '@portico/reader-core';
import type {ReaderDisplayControlsProps, ReaderDisplaySize} from '../../kit/reader/ReaderDisplay';
import type {ReaderMountAuthority} from './mount';

type ChoiceCopy<K extends string> = Readonly<{label: string; options: Readonly<Record<K, string>>}>;
export type ReadingDisplayCopy = Readonly<{
  themes: ChoiceCopy<ReadingPreferences['theme']>;
  fonts: Readonly<{label: string; options: Readonly<Record<ReadingPreferences['font'], Readonly<{label: string; compactLabel: string; description?: string}>>>}>;
  size: Readonly<{label: string; sliderLabel: string; smallerLabel: string; largerLabel: string; valueText: (value: number) => string}>;
  lineSpacing: ChoiceCopy<ReadingPreferences['lineSpacing']>;
  margins: ChoiceCopy<ReadingPreferences['margins']>;
  alignment: ChoiceCopy<ReadingPreferences['alignment']>;
  layout: ChoiceCopy<ReadingPreferences['layout']>;
  columns?: ChoiceCopy<'auto' | 'one'>;
  note: ReaderDisplayControlsProps['copy'];
  twoPages?: Readonly<{label: string; description?: string}>;
}>;
export type ReadingDisplayScale = Pick<ReaderDisplaySize, 'min' | 'max' | 'step' | 'ticks'>;

/** Presentation of the single core snapshot. These are open-book overrides;
 * this binding has no Settings reader, writer, raw key map or default values. */
export function readingDisplayControls({controller, authority, copy, scale, phone = false, reportError}: Readonly<{
  controller: ReadingController; authority: ReaderMountAuthority; copy: ReadingDisplayCopy;
  scale: ReadingDisplayScale; phone?: boolean; reportError: (code: ReaderErrorCode) => void;
}>): ReaderDisplayControlsProps {
  const initial = controller.getSnapshot(), {preferences} = initial;
  const change = (patch: Partial<ReadingPreferences>) => {
    const current = controller.getSnapshot();
    if (initial.status !== 'ready' || !authority.isCurrent() || current.status !== 'ready'
      || current.sourceId !== initial.sourceId || current.sourceRevision !== initial.sourceRevision) return;
    void controller.setOverride(patch).catch(error => {if (authority.isCurrent()) reportError(readerErrorCode(error));});
  };
  const choice = <K extends string>(label: ChoiceCopy<K>, values: readonly K[], value: K, changed: (value: K) => void) => ({
    label: label.label, value, options: values.map(value => ({value, label: label.options[value]})),
    onChange: (next: string) => {const selected = values.find(value => value === next); if (selected !== undefined) changed(selected);},
  });
  const themes = ['dark', 'black', 'sepia', 'light'] as const;
  const fonts = ['publisher', 'literata', 'manrope'] as const;
  const theme = choice(copy.themes, themes, preferences.theme, theme => change({theme}));
  const font = choice({label: copy.fonts.label, options: {publisher: copy.fonts.options.publisher.label,
    literata: copy.fonts.options.literata.label, manrope: copy.fonts.options.manrope.label}}, fonts, preferences.font, font => change({font}));
  const valueText = copy.size.valueText(preferences.textSize);
  return {
    themes: {...theme, options: themes.map(value => ({value, label: copy.themes.options[value], tone: value}))},
    fonts: {...font, options: fonts.map(value => ({value, ...copy.fonts.options[value], face: value}))},
    size: {...copy.size, ...scale, ticks: phone ? scale.ticks : undefined, value: preferences.textSize, valueText, summary: phone ? valueText : undefined,
      onChange: value => {if (Number.isFinite(value) && value >= scale.min && value <= scale.max && Number.isInteger((value - scale.min) / scale.step)) change({textSize: value});}},
    rows: [
      {id: 'lineSpacing', ...choice(copy.lineSpacing, ['tight', 'normal', 'loose'], preferences.lineSpacing, lineSpacing => change({lineSpacing}))},
      {id: 'margins', ...choice(copy.margins, ['narrow', 'normal', 'wide'], preferences.margins, margins => change({margins}))},
      {id: 'alignment', ...choice(copy.alignment, ['left', 'justified'], preferences.alignment, alignment => change({alignment}))},
      {id: 'layout', ...choice(copy.layout, ['pages', 'scroll'], preferences.layout, layout => change({layout})),
        options: [{value: 'pages', label: copy.layout.options.pages, icon: 'i-page'}, {value: 'scroll', label: copy.layout.options.scroll, icon: 'i-vscroll'}]},
      ...(copy.columns ? [{id: 'columns', ...choice(copy.columns, ['auto', 'one'], preferences.twoPages ? 'auto' : 'one', value => change({twoPages: value === 'auto'}))}] : []),
    ],
    twoPages: copy.twoPages ? {...copy.twoPages, checked: preferences.twoPages, onChange: twoPages => change({twoPages})} : undefined,
    copy: copy.note,
  };
}
