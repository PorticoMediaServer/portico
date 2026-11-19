import {useState, useSyncExternalStore} from 'react';
import type {PersonCredit, PersonPage, PersonRole} from '../../../../../../packages/client-core/src/people';
import type {WindowedCollection} from '../../../../../../packages/client-core/src/collections/windowed';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {createNativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {NativeSelection} from '../../../screens/selection/native-selection';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {ChoiceChip, PosterGridItem, PosterGridScrollerProps, StateRowProps} from '../../../kit/frames';
import type {PosterRowProps} from '../../../kit/layout';
import type {MenuEntry} from '../../../kit/overlays';
import {shareLinkActions} from '../../../screens/share/share-actions';
import {useTheme} from '../../../kit/theme';

const t = defaultI18n.t;

/** TV: one department of the person's credits, read as its own window and drawn as its own row (`register.tsx` builds them). */
export type PersonCreditGroup = Readonly<{id: string; department: string; total: number; collection: WindowedCollection<PersonCredit>}>;
/** What the route (`register.tsx`) gives a person's page: the person's read and credits, the person's whole count (the All
 * read's, kept while Cast or Crew is chosen), and on TV the departments' own windows. */
export type PersonPageProps = Readonly<{
  focusScope: string; page: PersonPage; role: PersonRole; collection: WindowedCollection<PersonCredit>; groups?: readonly PersonCreditGroup[];
  runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; selection?: NativeSelection;
  onRole: (role: PersonRole) => void; onBack: () => void; total?: number;
}>;

/** What a person's page shows (phone-detail d3 frame 7), from the old person screen's own reads: data only, so the touch
 * page and the TV page draw the same person. */
export type PersonView = Readonly<{
  /** Their photo, else their initials (frame 7: large initials; their films are the glow behind, not in the circle). */
  name: string; initials: string; portrait?: ArtworkSource;
  /** The glow behind the head: the first of their films with art (the old screen's ambient). */
  glow?: AmbientArtSource;
  /** Their departments, then Born / lived. */
  facts: readonly FactPart[];
  biography?: Readonly<{text: string; readMore: Readonly<{label: string; expanded: boolean; onPress: () => void}>}>;
  /** ⋯: Share and Copy link (absent when this server offers no link). */
  share?: Readonly<{label: string; items: readonly MenuEntry[]; open: boolean; onOpenChange: (open: boolean) => void}>;
  knownFor?: PosterRowProps;
  credits: Readonly<{title: string; count: string; choices: readonly ChoiceChip[];
    /** No credits under this filter, or the credits failed to read (Try again). */
    state?: StateRowProps;
    grid: PosterGridScrollerProps<PersonCredit>;
    /** TV (tv-detail frame 10, `f9`): a row per department ("Directing  5 titles"), each read on its own; one row of all
     * the credits when the person has one department. */
    rows: readonly PersonCreditRow[]}>;
  /** TV draws these apart (f9): the departments as the accent line over the name, the birthplace beside the dates. */
  roles?: string; birthPlace?: string;
  /** TV's circle without a photo: a blur of their posters behind the initials (f9). */
  mosaic: readonly AmbientArtSource[];
}>;
export type PersonCreditRow = Readonly<{id: string; title: string; count: string} & PosterGridScrollerProps<PersonCredit>>;

export function usePerson({page, role, collection, groups, runtime, actions, selection, onRole, total: personTotal}: PersonPageProps): PersonView {
  // A phone selects from a card's menu; a tablet leads the filters with Select (the old screen's rule).
  const phone = useTheme().form === 'phone';
  // A card's open menu and the selection redraw the cards that show them.
  useSyncExternalStore(actions.menuState.subscribe, actions.menuState.getSnapshot);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? zero);
  const credits = useSyncExternalStore(collection.subscribe, collection.getSnapshot);
  const [expanded, setExpanded] = useState(false), [sharing, setSharing] = useState(false);
  const person = page.person, known = person.knownFor;
  const born = person.birthDate ? defaultI18n.date(person.birthDate, 'long') : undefined, died = person.deathDate ? defaultI18n.date(person.deathDate, 'long') : undefined;
  const dates = born && died ? t('web.person.lived', {born, died}) : born ? t('web.person.born', {date: born}) : undefined;
  const shareItems: MenuEntry[] = shareLinkActions(runtime.shareScope?.(), 'person', {personId: person.id}, {notice: runtime.notice, report: runtime.reportError})
    .map(action => ({id: action.id, label: action.label, icon: action.icon, onSelect: action.onSelect}));
  const card = (entry: ContentEntry, group: 'known' | 'credits', caption?: string): PosterGridItem => {
    const pick = selection?.item(entry, group);
    return {title: entry.displayTitle || entry.title, caption, artwork: runtime.artwork(entry.posterUrl), selection: pick,
      onNavigate: () => actions.open(entry), onMore: anchor => actions.menu(entry, anchor, pick?.onEnter)};
  };
  const total = credits.total ?? page.pageInfo.total;
  const creditKey = (credit: PersonCredit) => `${credit.media.kind}:${credit.media.id}:${credit.creditKind}:${credit.role || credit.character}`;
  const creditCard = (credit: PersonCredit) => card(credit.media, 'credits', credit.character ? t('web.person.as', {character: credit.character}) : credit.role || undefined);
  // The heading's count is the person's (frame 7): a filter's read counts only its own credits, so the route keeps the All
  // read's total (`total`) and the heading shows it while Cast or Crew is chosen.
  return {
    name: person.name, initials: person.name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join(''),
    portrait: runtime.artwork(person.portraitUrl),
    glow: runtime.ambient(known.find(entry => entry.backdropUrl || entry.posterUrl)?.backdropUrl ?? known.find(entry => entry.posterUrl)?.posterUrl),
    facts: [...(person.roles.length ? [{id: 'roles', text: defaultI18n.list(person.roles), role: 'identity' as const}] : []), ...(dates ? [{id: 'dates', text: dates, role: 'context' as const}] : [])],
    biography: person.biography ? {text: person.biography,
      readMore: {label: t(expanded ? 'web.person.showLess' : 'web.person.readMore'), expanded, onPress: () => setExpanded(value => !value)}} : undefined,
    share: shareItems.length ? {label: t('next.overlays.share'), items: shareItems, open: sharing, onOpenChange: setSharing} : undefined,
    knownFor: known.length ? {title: t('web.person.knownFor'), small: true, items: known.map(entry => {
      const item = card(entry, 'known');
      return {key: `${entry.kind}:${entry.id}`, title: item.title, year: entry.year === undefined ? undefined : String(entry.year), artwork: item.artwork, selection: item.selection, onNavigate: item.onNavigate, onMore: item.onMore};
    })} : undefined,
    credits: {
      title: t('web.person.credits'), count: t('web.person.titlesOnServer', {count: personTotal ?? page.pageInfo.total}),
      choices: [
        // On a tablet, Select starts picking titles to act on together (a phone selects from a card's menu).
        ...(!phone && selection?.start ? [{id: 'select', label: t('next.selection.select'), icon: 'i-check' as const, testID: 'person-select', onPress: () => selection.start?.()}] : []),
        ...(['all', 'cast', 'crew'] as const).map(value => ({id: value, testID: `person-role-${value}`, selected: role === value, onPress: () => onRole(value),
          label: t(value === 'all' ? 'title.creditsAll' : value === 'cast' ? 'web.person.cast' : 'web.person.crew')}))],
      state: credits.status === 'error' ? {kind: 'error', icon: 'i-warn', title: presentError(credits.error, 'detail').title, action: {label: t('action.tryAgain'), onPress: () => collection.retry()}}
        : total === 0 ? {kind: 'empty', icon: 'i-user', title: t('web.person.noCredits'), reason: t(role === 'cast' ? 'web.person.noCast' : role === 'crew' ? 'web.person.noCrew' : 'web.person.noCreditsBody')} : undefined,
      grid: {collection, getKey: creditKey, testID: 'person-credits', item: creditCard},
      rows: groups?.length ? groups.map(group => ({id: group.id, title: group.department, count: t('web.person.titles', {count: group.total}), collection: group.collection,
        getKey: creditKey, item: creditCard, testID: `person-credits-${group.id}`}))
        : [{id: 'all', title: t('web.person.credits'), count: t('web.person.titles', {count: total}), collection, getKey: creditKey, item: creditCard, testID: 'person-credits'}],
    },
    roles: person.roles.length ? defaultI18n.list(person.roles) : undefined, birthPlace: person.birthPlace || undefined,
    mosaic: known.flatMap(entry => {const art = runtime.ambient(entry.posterUrl); return art ? [art] : [];}),
  };
}

const noSubscribe = () => () => {};
const zero = () => 0;
