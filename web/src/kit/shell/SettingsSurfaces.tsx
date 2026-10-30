import {useId, type CSSProperties, type ReactNode} from 'react';
import {createI18n} from '@i18n';
import {Icon, type IconName} from '../Icon';
import {BottomSheet} from '../overlays/BottomSheet';
import {Select} from '../controls/Select';
import type {BottomSheetProps} from '../overlays/BottomSheet';
import {Artwork} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {OwnerTag, ServerMark, type ServerRole, type ShellTranslate} from './ServerIdentity';
import {IdentityName, ProfileAvatarFace, type ProfileIdentity} from './AccountSurfaces';
import {settingsIconName} from '../settings/icons';
import styles from './settings.module.css';

const tDefault = createI18n().t;
export type ShellAction = Readonly<{id: string; label: string; icon?: IconName; iconOnly?: boolean; variant?: 'primary' | 'glass' | 'text'; onAction: () => void}>;
/** Navigation carries a setting's state, never a summary count. Major issues must be known for this exact row. */
export type SettingsNavEntry = Readonly<{id: string; label: string; icon: string; cluster?: string; stateValue?: string; majorIssue?: boolean}>;
export type SettingsSearchGroup = Readonly<{id: string; label: string; icon: string; area: 'account' | 'server'; configuration?: boolean; rows: readonly {id: string; label: string; description?: string}[]}>;
export function MatchText({text, query}: {text: string; query: string}) {
  const index = query ? text.toLocaleLowerCase().indexOf(query.toLocaleLowerCase()) : -1;
  return index < 0 ? <>{text}</> : <>{text.slice(0, index)}<mark>{text.slice(index, index + query.length)}</mark>{text.slice(index + query.length)}</>;
}
export function SettingsNav({accountName, serverName, viewerRole, account, server, selectedId, selectedSearchResult, query, onQueryChange, onNavigate, matches, onSearchResult, actions = [], layout = 'desktop', t = tDefault}: {
  accountName: string; serverName: string; viewerRole: ServerRole; account: readonly SettingsNavEntry[]; server: readonly SettingsNavEntry[];
  selectedId: string; selectedSearchResult?: Readonly<{pageId: string; rowId: string}>; query: string; onQueryChange: (value: string) => void; onNavigate: (id: string) => void;
  actions?: readonly ShellAction[]; matches?: readonly SettingsSearchGroup[]; onSearchResult: (pageId: string, rowId: string) => void; layout?: 'desktop' | 'phone'; t?: ShellTranslate;
}) {
  const rows = (entries: readonly SettingsNavEntry[]) => entries.map((entry, index) => <div key={entry.id} className={index > 0 && entry.cluster !== entries[index - 1]?.cluster ? styles.clusterStart : undefined}><button type="button" className={styles.navItem} aria-current={entry.id === selectedId ? 'page' : undefined} onClick={() => onNavigate(entry.id)}><span className={styles.navIcon}><Icon name={settingsIconName(entry.icon)} size={layout === 'phone' ? 18 : 19} /></span><span className={styles.navLabel}>{entry.label}</span>{entry.stateValue !== undefined && <span className={styles.teaser}>{entry.stateValue}</span>}{entry.majorIssue === true && <span className={styles.majorIssue} role="img" aria-label={t('next.shell.health.attention')} />}{layout === 'phone' && <Icon name="i-right" size={18} />}</button></div>);
  const visibleMatches = matches?.filter(group => group.configuration ? viewerRole === 'owner' : group.area === 'account' ? account.some(entry => entry.id === group.id) : viewerRole !== 'member' && server.some(entry => entry.id === group.id));
  const matchCount = visibleMatches?.reduce((count, group) => count + group.rows.length, 0) ?? 0;
  return <aside className={styles.settingsNav} data-layout={layout}>{layout === 'desktop' && <><h1>{t('next.shell.settings')}</h1><div className={styles.find}><Icon name="i-search" size={20} /><input type="search" value={query} onChange={event => onQueryChange(event.currentTarget.value)} placeholder={t('next.shell.searchSettings')} aria-label={t('next.shell.searchSettings')} />{query && <button type="button" className={styles.clearSearch} onClick={() => onQueryChange('')} aria-label={t('next.shell.clearSearch')}><Icon name="i-x" size={14} /></button>}</div></>}
    <nav aria-label={t('next.shell.settingsPages')}>{query && visibleMatches ? <div className={styles.searchResults} role="list" aria-label={t('next.shell.matchingSettings')}><div className={styles.matchCount}>{t('next.shell.settingsMatchCount', {count: matchCount})}</div>{visibleMatches.map(group => <section key={group.id} role="listitem"><h2><Icon name={settingsIconName(group.icon)} size={18} /><span><MatchText text={group.label} query={query} /></span><small>{t(group.area === 'account' ? 'next.shell.account' : 'next.shell.server')}</small></h2>{group.rows.map(row => <button key={row.id} type="button" aria-current={selectedSearchResult?.pageId === group.id && selectedSearchResult.rowId === row.id || undefined} onClick={() => onSearchResult(group.id, row.id)}><MatchText text={row.label} query={query} />{row.description && <small><MatchText text={row.description} query={query} /></small>}</button>)}</section>)}</div> : <><div className={styles.accountNav}><div className={styles.groupHeading}>{t('next.shell.account')}<small>{accountName}</small></div><div className={styles.navItems}>{rows(account)}</div></div>{viewerRole !== 'member' && server.length > 0 && <div className={styles.serverNav}><div className={styles.serverNavHeader}>{layout === 'phone' ? <span className={styles.phoneServerIcon}><Icon name="i-server" size={20} /></span> : <ServerMark size={34} />}<div>{layout === 'phone' ? <><small>{t('next.shell.server')}</small><b>{serverName}</b></> : <><b>{t('next.shell.server')}</b><small>{serverName}</small></>}</div>{viewerRole === 'owner' ? <OwnerTag size={layout === 'phone' ? 'phone' : 'standard'} /> : <span className={styles.roleBadge}>{t('next.shell.administrator')}</span>}</div>{rows(server)}</div>}</>}</nav>
    {layout === 'phone' && !query && actions.length > 0 && <div className={styles.accountNav}><div className={styles.navItems}>{actions.map(action => <div key={action.id}><button type="button" className={styles.navItem} onClick={action.onAction}><span className={styles.navIcon}>{action.icon && <Icon name={action.icon} size={18} />}</span><span className={styles.navLabel}>{action.label}</span><Icon name="i-right" size={18} /></button></div>)}</div></div>}
  </aside>;
}

export function LargeTitleHeader({title, actions, className}: {title: string; actions?: ReactNode; className?: string}) {
  return <header className={[styles.largeTitle, className].filter(Boolean).join(' ')}><h1>{title}</h1>{actions}</header>;
}
export function PushedNavBar({title, backLabel, onBack, trailing, safeArea = true}: {title?: string; backLabel: string; onBack: () => void; trailing?: ReactNode; safeArea?: boolean}) {
  return <header className={styles.pushedNav} data-safe-area={safeArea || undefined}><button type="button" onClick={onBack}><Icon name="i-left" size={24} />{backLabel}</button>{title && <span className={styles.pushedTitle}>{title}</span>}<div className={styles.pushedTrailing}>{trailing}</div></header>;
}
export function ShortcutTiles({items, compact = false}: {items: readonly ShellAction[]; compact?: boolean}) {
  return <div className={styles.shortcuts} data-compact={compact || undefined}>{items.map(item => <button type="button" key={item.id} onClick={item.onAction}>{item.icon && <Icon name={item.icon} size={22} />}{item.label}</button>)}</div>;
}
export type SettingsSheetProps = Omit<BottomSheetProps, 'title' | 'children'> & {
  profile: ProfileIdentity; accountDescription: string; onProfile: () => void; query: string; onQueryChange: (value: string) => void;
  shortcuts: readonly ShellAction[]; children: ReactNode; t?: ShellTranslate;
};
export function SettingsSheet({profile, accountDescription, onProfile, query, onQueryChange, shortcuts, children, t = tDefault, ...props}: SettingsSheetProps) {
  return <BottomSheet {...props} title={t('next.shell.settings')} showCloseButton className={styles.settingsSheet} headerClassName={styles.sheetHeader} bodyClassName={styles.sheetBody}>
    <label className={styles.sheetFind}><Icon name="i-search" size={19} /><input type="search" value={query} onChange={event => onQueryChange(event.currentTarget.value)} aria-label={t('next.shell.searchSettings')} placeholder={t('next.shell.searchSettings')} /></label>
    <button type="button" className={styles.profileCard} onClick={onProfile}><ProfileAvatarFace initial={profile.initial} art={profile.art} size={54} /><span><b><IdentityName person={profile} /></b><small>{accountDescription}</small></span><Icon name="i-right" size={20} /></button>
    <ShortcutTiles items={shortcuts} compact />{children}
  </BottomSheet>;
}

export function SettingsRow({label, help, children, dependent = false, found = false, stacked = false, id, className}: {
  label: ReactNode; help?: ReactNode; children: ReactNode; dependent?: boolean; found?: boolean; stacked?: boolean; id?: string; className?: string;
}) {
  return <div id={id} className={[styles.row, className].filter(Boolean).join(' ')} data-dependent={dependent || undefined} data-found={found || undefined} data-stacked={stacked || undefined}><div className={styles.rowLabel}><b>{label}</b>{help && <p>{help}</p>}</div><div className={styles.rowControl}>{children}</div></div>;
}
export function DefaultHint({value, effectiveSource, onReset, variant = 'default', t = tDefault}: {value: string; effectiveSource: string; onReset: () => void; variant?: 'default' | 'changed'; t?: ShellTranslate}) {
  if (effectiveSource === 'default') return null;
  return <span className={styles.defaultHint} data-variant={variant}>{variant === 'changed' ? t('next.shell.changed') : t('next.shell.defaultHint', {value})} <button type="button" onClick={onReset}>{t('next.shell.reset')}</button></span>;
}

export type SettingOption = Readonly<{value: string; label: string; description?: string}>;
export function ChoiceMatrix({choices, rows, onChange, label, disabled}: {choices: readonly SettingOption[]; rows: readonly {id: string; label: string; icon?: IconName; value: string; hint?: ReactNode}[]; onChange: (id: string, value: string) => void; label: string; disabled?: boolean}) {
  const instance = useId();
  return <div className={styles.matrix} aria-label={label} style={{'--choice-columns': choices.length} as CSSProperties}>
    <div className={styles.matrixHead} aria-hidden="true"><span />{choices.map(choice => <span key={choice.value}>{choice.label}{choice.description && <small>{choice.description}</small>}</span>)}</div>
    {rows.map(row => <fieldset key={row.id} className={styles.matrixRow} disabled={disabled}><legend>{row.label}</legend><span className={styles.matrixLabel}>{row.icon && <Icon name={row.icon} size={20} />}{row.label}{row.hint}</span>{choices.map(choice => <label key={choice.value} className={styles.matrixChoice}><input type="radio" name={`${instance}-${row.id}`} value={choice.value} checked={row.value === choice.value} onChange={() => {if (!disabled) onChange(row.id, choice.value);}} aria-label={choice.label} /><span className={styles.radio} /></label>)}<div className={styles.phoneChoice}><Select aria-label={row.label} disabled={disabled} options={choices} value={row.value} onValueChange={value => {if (!disabled) onChange(row.id, value);}} /></div></fieldset>)}
  </div>;
}
export function ChoiceCards({label, options, value, onChange}: {label: string; options: readonly SettingOption[]; value: string; onChange: (value: string) => void}) {
  const name = useId();
  return <fieldset className={styles.choiceCards}><legend>{label}</legend>{options.map(option => <label key={option.value} className={styles.choiceCard} data-selected={value === option.value || undefined}><input type="radio" name={name} value={option.value} checked={value === option.value} onChange={() => onChange(option.value)} /><span className={styles.radio} /><span><b>{option.label}</b>{option.description && <span>{option.description}</span>}</span></label>)}</fieldset>;
}
export type LanguageValue = Readonly<{id: string; label: string}>;
export function LanguageList({languages, onRemove, onMove, onAdd, t = tDefault}: {languages: readonly LanguageValue[]; onRemove: (id: string) => void; onMove: (id: string, direction: -1 | 1) => void; onAdd: () => void; t?: ShellTranslate}) {
  return <div className={styles.languages}>{languages.map((language, index) => <span className={styles.language} key={language.id}><i>{index + 1}</i>{language.label}<button type="button" aria-label={t('next.shell.removeLanguage', {language: language.label})} onClick={() => onRemove(language.id)}><Icon name="i-x" size={16} /></button><span className={styles.reorder}>{index > 0 && <button type="button" aria-label={t('next.shell.moveLanguageUp', {language: language.label})} onClick={() => onMove(language.id, -1)}><Icon name="i-arrow-up" size={16} /></button>}{index < languages.length - 1 && <button type="button" aria-label={t('next.shell.moveLanguageDown', {language: language.label})} onClick={() => onMove(language.id, 1)}><Icon name="i-arrow-down" size={16} /></button>}</span></span>)}<button type="button" className={styles.addLanguage} onClick={onAdd}><Icon name="i-plus" size={18} />{t('next.shell.addLanguage')}</button></div>;
}
export function SubtitlePreview({source, size, background, t = tDefault}: {source: ArtworkSource; size: 'small' | 'medium' | 'large' | 'extra-large'; background: 'none' | 'translucent' | 'opaque'; t?: ShellTranslate}) {
  return <div className={styles.subtitlePreview} data-size={size} data-background={background}><Artwork source={source} alt="" position="center 30%" width="100%" height="100%" /><span className={styles.previewLabel}>{t('next.shell.subtitlePreview')}</span><span className={styles.cue}>{t('next.shell.subtitleSample')}</span></div>;
}
