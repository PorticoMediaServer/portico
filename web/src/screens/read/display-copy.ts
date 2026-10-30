import type {I18n} from '@i18n';
import type {ReadingDisplayCopy} from './display';

/** Frame wording is separate from core values and the server's size domain. */
export function readingDisplayCopy(i18n: I18n, presentation: 'desktop' | 'phone' | 'tablet', maximumSize: number): ReadingDisplayCopy {
  const {t} = i18n;
  return {
    themes: {label: t('next.reader.page'), options: {
      dark: t('next.settingsSpec.account.reading.groups.0.rows.0.control.options.0.label'),
      black: t('next.settingsSpec.account.reading.groups.0.rows.0.control.options.1.label'),
      sepia: t('next.settingsSpec.account.reading.groups.0.rows.0.control.options.2.label'),
      light: t('next.settingsSpec.account.reading.groups.0.rows.0.control.options.3.label'),
    }},
    fonts: {label: t('next.reader.font'), options: {
      publisher: {label: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.0.label'), compactLabel: t('next.reader.publisherCompact'),
        description: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.0.hint')},
      literata: {label: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.1.label'), compactLabel: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.1.label')},
      manrope: {label: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.2.label'), compactLabel: t('next.settingsSpec.account.reading.groups.0.rows.1.control.options.2.label')},
    }},
    size: {label: t('next.reader.size'), sliderLabel: t('next.reader.textSize'), smallerLabel: t('next.reader.smaller'), largerLabel: t('next.reader.larger'),
      valueText: value => presentation === 'desktop' ? i18n.number(value) : t('next.reader.sizePosition', {value, count: maximumSize})},
    lineSpacing: {label: t('next.settingsSpec.account.reading.groups.1.rows.0.label'), options: {
      tight: t('next.settingsSpec.account.reading.groups.1.rows.0.control.options.0.label'),
      normal: t('next.settingsSpec.account.reading.groups.1.rows.0.control.options.1.label'),
      loose: t('next.settingsSpec.account.reading.groups.1.rows.0.control.options.2.label'),
    }},
    margins: {label: t('next.settingsSpec.account.reading.groups.1.rows.1.label'), options: {
      narrow: t('next.settingsSpec.account.reading.groups.1.rows.1.control.options.0.label'),
      normal: t('next.settingsSpec.account.reading.groups.1.rows.1.control.options.1.label'),
      wide: t('next.settingsSpec.account.reading.groups.1.rows.1.control.options.2.label'),
    }},
    alignment: {label: t('next.settingsSpec.account.reading.groups.1.rows.2.label'), options: {
      left: t('next.settingsSpec.account.reading.groups.1.rows.2.control.options.0.label'),
      justified: t('next.settingsSpec.account.reading.groups.1.rows.2.control.options.1.label'),
    }},
    layout: {label: t('next.reader.layout'), options: {
      pages: t('next.settingsSpec.account.reading.groups.1.rows.3.control.options.0.label'),
      scroll: t('next.settingsSpec.account.reading.groups.1.rows.3.control.options.1.label'),
    }},
    columns: presentation === 'desktop' ? {label: t('next.reader.columns'), options: {auto: t('next.reader.columnsAuto'), one: t('next.reader.columnsOne')}} : undefined,
    twoPages: presentation === 'tablet' ? {label: t('next.reader.twoPagesLandscape'), description: t('next.reader.twoPagesPortrait')} : undefined,
    note: {sample: t('next.reader.sample'), sizeSample: t('next.reader.sizeSample'), sessionNote: t('next.reader.sessionNote'), defaultsNote: t('next.reader.defaultsNote'),
      openSettings: presentation === 'phone' ? undefined : t('next.reader.openSettings')},
  };
}
