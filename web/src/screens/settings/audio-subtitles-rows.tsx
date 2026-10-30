import {useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import type {SettingsRow, SettingsValue} from '@core/presentation/settings-spec.ts';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {SettingsOrderedList, type SettingPresentation} from '../../kit/settings';
import styles from './audio-subtitles.module.css';
import {maxPreferredLanguages, preferredLanguageChoices, preferredLanguageEntryName, type PreferredLanguageKind} from './audio-subtitles-model';

const t = defaultI18n.t;
export function preferredLanguageKind(rowId: string): PreferredLanguageKind | undefined {
  return rowId === 'audioSubtitles.preferredAudio' ? 'audio' : rowId === 'audioSubtitles.preferredSubtitles' ? 'subtitles' : undefined;
}
export function isSubtitlePreview(rowId: string): boolean { return rowId === 'audioSubtitles.subtitlePreview'; }

function languageCodes(value: SettingsValue | undefined): readonly string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}
function addLabelKey(kind: PreferredLanguageKind) {
  return kind === 'audio' ? 'next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.addLabel' as const
    : 'next.settingsSpec.account.audio-subtitles.groups.1.rows.0.control.addLabel' as const;
}
function emptyKey(kind: PreferredLanguageKind) {
  return kind === 'audio' ? 'next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.empty' as const
    : 'next.settingsSpec.account.audio-subtitles.groups.1.rows.0.control.empty' as const;
}

/** The drawn ordered language list: numbers, drag or Move buttons, Remove and Add. */
export function AudioSubtitleLanguageList({row, state, locale, appLocale, onChange, onError}: {
  row: SettingsRow; state: SettingPresentation; locale: string; appLocale: string; onChange: (value: SettingsValue) => void; onError: (error: unknown) => void;
}) {
  const kind = preferredLanguageKind(row.id);
  const [open, setOpen] = useState(false), [query, setQuery] = useState('');
  if (!kind) return null;
  const codes = languageCodes(state.value);
  const items = codes.map(code => ({id: code, label: preferredLanguageEntryName(code, kind, locale, appLocale)}));
  const choices = preferredLanguageChoices(kind, codes, locale, appLocale);
  const hits = choices.filter(choice => !query.trim() || choice.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()) || choice.code.toLowerCase() === query.trim().toLowerCase());
  const pick = (code: string) => {onChange([...codes, code]); setOpen(false); setQuery('');};
  return <>
    <SettingsOrderedList items={items} label={t(row.label)} numbered
      onReorder={ids => onChange([...ids])} onRemove={id => onChange(codes.filter(code => code !== id))} onError={onError}
      empty={t(emptyKey(kind))}
      add={codes.length < maxPreferredLanguages ? <Button className={styles.addLanguage} variant="glass" size="small" icon="i-plus" iconSize={18} onClick={() => setOpen(true)}>{t(addLabelKey(kind))}</Button> : undefined} />
    <Dialog open={open} onOpenChange={value => {setOpen(value); if (!value) setQuery('');}} title={t(addLabelKey(kind))}
      description={t('next.settings.audioSubtitles.addHint')} footer={<Button variant="glass" size="small" onClick={() => setOpen(false)}>{t('next.settings.cancel')}</Button>}>
      <TextField variant="search" autoFocus aria-label={t('next.settings.audioSubtitles.search')} placeholder={t('next.settings.audioSubtitles.search')} value={query} onChange={event => setQuery(event.currentTarget.value)} />
      <div role="listbox" aria-label={t(addLabelKey(kind))} className={styles.pickerList}>
        {hits.map(choice => <button key={choice.code} type="button" role="option" aria-selected={false} className={styles.pickerRow} onClick={() => pick(choice.code)}>
          <span>{choice.label}</span>{choice.code.startsWith('@') ? null : <small className={styles.pickerCode}>{choice.code}</small>}
        </button>)}
        {!hits.length && <p className={styles.pickerEmpty}>{t(choices.length ? 'next.settings.audioSubtitles.noMatch' : 'next.settings.audioSubtitles.allAdded')}</p>}
      </div>
    </Dialog>
  </>;
}

/** The drawn subtitle preview: the player's own palette over a still-like backdrop. */
export function SubtitlePreview({paint, text}: {paint: {color: string; backgroundColor: string; textShadow: string; scale: number; position: 'bottom' | 'raised' | 'top'}; text: string}) {
  return <div className={styles.preview} role="img" aria-label={t('next.settings.audioSubtitles.previewLabel', {text})}>
    <div className={styles.previewCaption} data-position={paint.position}>
      <span className={styles.previewText} style={{'--preview-size': `calc(5.2cqh * ${paint.scale})`, '--preview-color': paint.color, '--preview-background': paint.backgroundColor, '--preview-shadow': paint.textShadow} as CSSProperties}>{text}</span>
    </div>
  </div>;
}
