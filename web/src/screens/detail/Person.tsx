import {t, locale} from './text';
import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import type {PersonPage, PersonCredit} from '@core/people.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {Chip, Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {AmbientArt, PersonCircle} from '../../kit/artwork';
import {PosterCard} from '../../kit/cards';
import {WindowedGrid, Lockup, Shelf} from '../../kit/layout';
import {StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import type {CardTextPresentation} from '../../kit/cards/types';
import {DetailLoading, ScreenFailure, TitleRow} from './Detail';
import {entryCard} from './model';
import {creditRole, type PersonBindings} from './person-bindings';
import styles from './person.module.css';
import {TitleProse} from '../../kit/title/TitleParts';
import {ScreenProblem} from './ScreenProblem';
import {runScreenCommand} from './context';

const date = (value: string) => /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(value)) ? new Intl.DateTimeFormat(locale(), {year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC'}).format(new Date(`${value}T12:00:00Z`)) : undefined;
const groupHeading = (kind: string | undefined) => t(kind === 'movie' ? 'search.chip.movies' : kind === 'show' ? 'search.chip.shows' : 'web.person.credits');

export type PersonScreenProps = {binding: PersonBindings; personId: string};
export function PersonScreen({binding, personId}: PersonScreenProps) {
  const snapshot = useSyncExternalStore(binding.subscribe, binding.getSnapshot, binding.getSnapshot);
  const [full, setFull] = useState(false), width = useShellWidth(), phone = width < 1024;
  useEffect(() => setFull(false), [personId]);
  if (!snapshot.page) return snapshot.phase === 'error' ? <ScreenProblem context={binding.context} error={snapshot.error} retry={() => runScreenCommand(binding.context, () => binding.retry(snapshot))} /> : <DetailLoading />;
  const page = snapshot.page, {person} = page, art = binding.context.artwork;
  const initials = person.name.split(/\s+/).filter(Boolean).slice(0, 2).map(word => Array.from(word)[0]).join('');
  const born = date(person.birthDate), died = date(person.deathDate);
  const covers = person.knownFor.length > 0 ? Array.from({length: 4}, (_, index) => person.knownFor[index % person.knownFor.length]).flatMap(entry => { const source = art.foreground(entry.posterUrl, 320, 2 / 3); return source ? [source] : []; }) : [];
  const titles = page.pageInfo.total;
  const hasMeta = Boolean(born || person.birthPlace || titles > 0);
  const hasBiography = Boolean(person.biography), biographySource = person.biographySource;
  const roles = person.roles.join(', ');
  const bornText = born ? (died ? t('web.person.lived', {born, died}) : t('web.person.born', {date: born})) : undefined;
  const roleChips = <div className={phone ? styles.phoneChips : styles.chips}>{(['all', 'cast', 'crew'] as const).map(role => <Chip key={role} variant={phone ? 'default' : 'quick'} selected={snapshot.role === role} onClick={() => runScreenCommand(binding.context, () => { binding.current(page); return binding.select(person.id, role); })}>{t(role === 'all' ? 'web.search.all' : role === 'cast' ? 'web.person.cast' : 'web.person.crew')}</Chip>)}</div>;
  return <article className={styles.person} data-person-screen data-phone={phone || undefined}>
    {phone && <div className={styles.phoneBar} data-person-phone-bar><Button variant="icon" icon="i-left" iconOnly aria-label={t('accounts.signIn.back')} onClick={() => runScreenCommand(binding.context, () => {binding.current(page);binding.context.effects.back?.();})} /></div>}
    <header className={styles.hero} data-person-hero>
      <AmbientArt className={styles.wash} treatment="artist" sources={person.knownFor.slice(0, 3).flatMap(entry => { const source = art.ambient(entry.backdropUrl ?? entry.posterUrl); return source ? [source] : []; })} />
      <div className={styles.portrait} data-person-disc><PersonCircle covers={phone ? [] : covers} initials={initials} alt={person.name} initialsSize={phone ? 38 : 128} /></div>
      <div className={styles.copy} data-person-copy>
        {phone ? <>
          <h1 className={styles.phoneName}>{person.name}</h1>
          {(roles || bornText) && <p className={styles.phoneMeta} data-person-meta>{roles && <strong>{roles}</strong>}{bornText && <span>{bornText}</span>}</p>}
          {hasBiography ? <TitleProse className={styles.phoneBio} data-person-bio data-full={full || undefined}>{person.biography}</TitleProse> : <TitleProse className={styles.phoneBio} data-person-bio>{t('web.person.noBio')}</TitleProse>}
          {hasBiography && <Button variant="text" size="small" className={styles.phoneReadMore} onClick={() => runScreenCommand(binding.context, () => { binding.current(page); setFull(value => !value); })}>{t(full ? 'web.person.showLess' : 'web.person.readMore')}</Button>}
        </> : <>
          {person.roles.length > 0 && <p className={styles.roles} data-person-roles>{roles}</p>}
          <Lockup title={person.name} platform="desktop" />
          {hasMeta && <p className={styles.meta} data-person-meta>
            {bornText && <strong>{bornText}</strong>}
            {person.birthPlace && <span>{person.birthPlace}</span>}
            {titles > 0 && <span>{t('web.person.titlesOnServer', {count: titles})}</span>}
          </p>}
          {hasBiography ? <TitleProse className={styles.biography} data-person-bio data-full={full || undefined}>{person.biography}</TitleProse> : <TitleProse className={styles.biography} data-person-bio>{t('web.person.noBio')}</TitleProse>}
          {(hasBiography || biographySource) && <div className={styles.bioFoot} data-person-bio-foot>
            {hasBiography && <Button variant="text" size="small" className={styles.readMore} onClick={() => runScreenCommand(binding.context, () => { binding.current(page); setFull(value => !value); })}>{t(full ? 'web.person.showLess' : 'web.person.readMore')}<Icon name={full ? 'i-up' : 'i-down'} size={16} /></Button>}
            {biographySource && <small className={styles.source} data-person-source>{t('web.person.biographyFrom', {source: biographySource})}</small>}
          </div>}
        </>}
      </div>
    </header>
    {phone
      ? snapshot.role === 'all' && person.knownFor.length > 0 && titles > person.knownFor.length && <PhoneKnownFor binding={binding} page={page} />
      : snapshot.role === 'all' && person.knownFor.length > 0 && titles > person.knownFor.length && <TitleRow binding={binding} heading={t('web.person.knownFor')} entries={person.knownFor} guard={() => binding.current(page)} />}
    {phone
      ? <div><div className={styles.phoneCreditsHeader} data-person-credits-header><h2>{t('web.person.credits')}</h2><span>{t('web.person.titlesOnServer', {count: titles})}</span></div>{roleChips}</div>
      : <div className={styles.toolbar} data-person-toolbar><h2 className={styles.toolbarHeading} data-person-toolbar-heading>{t('web.person.onServer')}</h2><span className={styles.count} data-person-count>{t('web.person.titles', {count: titles})}</span>{roleChips}</div>}
    {titles === 0 && <div className={phone ? undefined : styles.credits}><StateView title={t('web.person.noCredits')} body={t('web.person.noCreditsBody')} /></div>}
    {phone
      ? titles > 0 && <PersonCredits binding={binding} page={page} total={titles} heading={t('web.person.credits')} phone showHeading={false} />
      : page.groups.some(group => group.kind && group.total > 0) && snapshot.role === 'all'
        ? <div className={styles.groups} data-person-groups>{page.groups.filter(group => group.kind && group.total > 0).map(group => <PersonCredits key={group.id} binding={binding} page={page} kind={group.kind} total={group.total} heading={groupHeading(group.kind)} phone={false} />)}</div>
        : titles > 0 && <div className={styles.credits} data-person-credits><PersonCredits binding={binding} page={page} total={titles} heading={t('web.person.credits')} phone={false} /></div>}
    {!phone && titles > 0 && <p className={styles.note} data-person-note><Icon name="i-info" size={18} />{t('web.person.serverNote')}</p>}
  </article>;
}

function PhoneKnownFor({binding, page}: {binding: PersonBindings; page: PersonPage}) {
  const open = (entry: ContentEntry) => runScreenCommand(binding.context, () => binding.openKnownFor(page, entry));
  return <div data-person-knownfor><Shelf focusScope={binding} heading={t('web.person.knownFor')} items={page.person.knownFor} getKey={entry => entry.id} platform="phone-web" style={{marginTop: 18}}
    renderItem={entry => <PosterCard className={styles.knownCard} model={entryCard(entry, {t, locale: locale()})} context="detail"
      artwork={binding.context.artwork.foreground(entry.posterUrl, 380, 2 / 3)} watched={entry.watched} onNavigate={() => open(entry)} />} /></div>;
}

type PersonCreditsProps = {binding: PersonBindings; page: PersonPage; kind?: string; total: number; heading: string; phone: boolean; showHeading?: boolean};
type CreditLease = ReturnType<PersonBindings['acquireCredits']>;
function PersonCredits(props: PersonCreditsProps) {
  const {binding, page, kind} = props;
  const [published, publish] = useState<Readonly<{binding: PersonBindings; page: PersonPage; kind?: string; lease: CreditLease}>>();
  useLayoutEffect(() => {
    const lease = binding.acquireCredits(page, kind);
    publish({binding, page, kind, lease});
    return lease.release;
  }, [binding, page, kind]);
  const owned = published && published.binding === binding && published.page === page && published.kind === kind && published.lease.current() ? published.lease : undefined;
  return owned ? <PersonCreditGrid {...props} lease={owned}/> : null;
}
function PersonCreditGrid({binding, page, kind, total, heading, phone, showHeading = true, lease}: PersonCreditsProps & {lease: CreditLease}) {
  const collection = lease.collection;
  const state = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  const columns = Math.max(1, Math.min(total, 4));
  const grouped = kind !== undefined && !phone;
  const cardWidth = phone ? 112.66 : 190, gap = phone ? 10 : 16, rowHeight = phone ? 211 : 354, rowGap = phone ? 16 : 30;
  const captions = (credit: PersonCredit) => credit.roles.length > 0
    ? credit.roles.map((role, index) => creditRole(role, `role-${index}`))
    : [];
  const grid = <WindowedGrid className={phone ? styles.phoneGrid : grouped ? styles.grid : styles.wideGrid} collection={collection} label={heading} cardWidth={cardWidth} rowHeight={rowHeight} rowGap={rowGap} gap={gap} height={phone ? 438 : grouped ? rowHeight : 'calc(100vh - 240px)'} style={phone ? {width: 358, marginLeft: 16} : grouped ? {width: columns * cardWidth + (columns - 1) * gap} : undefined}
    renderPlaceholder={index => <span className={styles.placeholder} key={index} />}
    renderItem={(credit: PersonCredit) => {
      const model = entryCard(credit.media, {t, locale: locale()}), roles = captions(credit);
      const extraCaption = roles.map(role => [role.part]);
      const extraPresentation = phone ? [] : roles.map(role => role.presentation);
      const textPresentation: CardTextPresentation | undefined = !phone && extraPresentation.some(Boolean) ? {extraCaption: extraPresentation} : undefined;
      return <PosterCard className={phone ? styles.phoneCreditCard : undefined} model={{title: model.title, caption: [], progress: model.progress}} context="detail" textPresentation={textPresentation}
        artwork={binding.context.artwork.foreground(credit.media.posterUrl, 380, 2 / 3)} watched={credit.media.watched} extraCaption={extraCaption}
        onNavigate={() => runScreenCommand(binding.context, () => {if (!lease.current()) throw new DOMException('Person credit opening retired', 'AbortError');binding.open(page, credit);})} />;
    }} />;
  return <section className={phone ? styles.phoneGroup : styles.group} data-person-group={kind ?? 'all'}>
    {showHeading && <h2>{heading}{total !== undefined && <span className={styles.groupCount}>{total}</span>}</h2>}
    {state.status === 'error' && <ScreenFailure retry={() => {if (lease.current()) collection.retry();}} />}
    {grouped ? <div className={styles.track}>{grid}</div> : grid}
  </section>;
}
