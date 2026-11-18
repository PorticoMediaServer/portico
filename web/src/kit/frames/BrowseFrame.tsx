import type {CSSProperties, ReactNode} from 'react';
import {frameSpec} from '@design';
import type {FactPart} from '@core/presentation/kit-model';
import {FiltersSurface, LibraryHeader, StickyBrowseHeader} from '../browse';
import {Tabs, type ChoiceItem} from '../controls';
import {Hero, type HeroCandidate, type HeroProps} from '../layout';
import {useFrameForm, usePhone} from '../overlays/shared';
import {Skeleton} from '../states';
import {useCoarsePointer} from '../theme';
import {PageHeader, PageStateFrame, type PageHeaderProps, type PageStateProps} from './frames';
import frames from './frames.module.css';
import styles from './browse.module.css';

/** The header regions (`frameRegions.browse`): a library's title is its switcher; a pushed list has Back and a plain title. */
export type BrowseHeaderProps = PageHeaderProps & Readonly<{
  /** The kit `LibrarySwitcher`: a round chevron beside the title, the centred name at phone width (`compact`). */
  switcher?: ReactNode;
  /** The kit `LibraryMenu` (⋯): Save this view, Mark all as watched, Edit pins, and the owner's Library settings and Scan. */
  menu?: ReactNode;
  /** The library's counts under its title ("10 artists  10 albums  40 songs"): wide headers only, never the phone bar. */
  counts?: readonly FactPart[];
  /** The page's tabs, Discover / Movies / Collections (Music: Discover / Artists / Albums / Songs / Genres), from the gutter with their rule across the page. */
  tabs?: Readonly<{items: readonly ChoiceItem[]; value: string; onValueChange: (value: string) => void}>;
  /** A pushed list's one line saying why these titles are here (desktop-browse seeall). */
  intro?: ReactNode;
  /** Labelled buttons under the intro: Play and Shuffle on See all. */
  actions?: ReactNode;
}>;

/** The Filters region: what the fields say and do. The frame picks the surface: a sheet at phone width, a side panel over the
 * page, a pane docked beside the grid on a touch tablet. */
export type BrowseFiltersProps = Readonly<{
  open: boolean; onClose: () => void; onClear: () => void;
  /** "6 movies": the panel reads "6 movies match", the sheet's primary "Show 6 movies". */
  count?: string;
  save?: Readonly<{canSave: boolean; onSave: () => void}>;
  /** The fields: the kit `FilterBuilder`. */
  children: ReactNode;
}>;

export type BrowseFrameProps = Readonly<{
  /** The rows: one flexible region. Each child is a kit `Shelf` (it draws its own heading); the frame spaces them. A row with no
   * items is left out whole. */
  children?: ReactNode;
  /** In place of rows, the other flexible region: a grid or list that scrolls itself (kit `WindowedGrid` at `height="100%"`). */
  grid?: ReactNode;
  /** The kit `AlphabetRail`, beside the grid in the right gutter. */
  alphabet?: ReactNode;
  /** The kit `BrowseToolbar` (Filters, quick chips, count, sort, view, Save view, applied filters), fixed under the header. */
  toolbar?: ReactNode;
  filters?: BrowseFiltersProps;
  /** The whole page is loading for the first time, or can't load. Loading keeps the header, tabs and toolbar and draws the
   * grid's or rows' boxes; a failure keeps the header. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}> & (
  | Readonly<{header: BrowseHeaderProps; hero?: never}>
  /** Home: the hero leads, under the shell's nav. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, See all, Live TV, Saved, Search results. Its regions are `frameRegions.browse` and `.home`
 * (packages/design). The frame owns the header, gutters, the space between regions and rows, and page-level states; the shell
 * owns the nav and tab bar around it, and the window owns the backdrop. */
export function BrowseFrame({header, hero, toolbar, grid, alphabet, filters, state, children}: BrowseFrameProps) {
  // The window's form, kept current on resize (it was read from the window during render).
  const form = useFrameForm(), phone = form === 'phone', coarse = useCoarsePointer();
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure, gridded = !!grid || loading && !!toolbar;
  // A touch tablet docks Filters beside the grid (tablet 4); a pointer gets the side panel over the page (desktop-browse filters).
  const docked = coarse && !!filters?.open && form === 'expanded';
  const dock = docked ? {'--browse-dock': `${frameSpec.browse.filtersWidth.expanded}px`} as CSSProperties : undefined;
  return <div style={dock} className={`${frames.frame} ${styles.browse}`} data-hero={hero ? true : undefined} data-header={header ? true : undefined} data-grid={gridded || undefined} data-docked={docked || undefined}>
    {header ? <BrowseHeader {...header} phone={phone} /> : hero ?? <div className={styles.top} />}
    {toolbar}
    {failure ? <PageStateFrame {...failure} /> : gridded
      ? <div className={styles.content}>{loading ? <GridSkeleton /> : grid}{alphabet && !loading && <div className={styles.alphabet}>{alphabet}</div>}</div>
      : <div className={styles.rows}>{loading ? <RowsSkeleton /> : children}</div>}
    {filters && <FiltersSurface form={phone ? 'sheet' : docked ? 'docked' : 'panel'} medium={form === 'medium'} open={filters.open} onClose={filters.onClose}
      onClear={filters.onClear} count={filters.count} save={phone ? filters.save : undefined}>{filters.children}</FiltersSurface>}
  </div>;
}

/** The header (frameRegions.browse `header.*`): a library is the phone's compact bar (‹ Libraries, the switcher centred, ⋯, the
 * tabs inside it) or the wide title with its switcher, Select and ⋯ at the right, and the tabs under it; a plain page is rule 3's header. */
function BrowseHeader({title, back, trailing, switcher, menu, counts, tabs: tabItems, intro, actions, phone}: BrowseHeaderProps & {phone: boolean}) {
  const tabs = tabItems && <Tabs items={tabItems.items} value={tabItems.value} onValueChange={tabItems.onValueChange} phone={phone} listClassName={styles.tabsInset} />;
  if (!switcher) return <><PageHeader title={title} back={back} trailing={trailing} />{intro && <p className={`${frames.intro} ${styles.intro}`}>{intro}</p>}{actions && <div className={styles.actions}>{actions}</div>}{tabs}</>;
  if (phone) return <StickyBrowseHeader name={title} switcher={switcher} menu={menu} onLibraries={back?.onPress ?? (() => {})} sticky={false}>{tabs}</StickyBrowseHeader>;
  return <LibraryHeader name={title} counts={counts} switcher={switcher} menu={trailing || menu ? <>{trailing}{menu}</> : undefined}>{tabs}</LibraryHeader>;
}

/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const row = (width: string, ratio: string, sheen: boolean) => <div className={styles.skeletonRow}><Skeleton quiet line width="var(--card-poster-width)" />
    <div className={styles.skeletonCards}>{Array.from({length: 9}, (_, index) => <Skeleton quiet key={index} width={width} sheen={sheen && index === 0} style={{aspectRatio: ratio}} />)}</div></div>;
  return <div aria-hidden="true" className={styles.rows}>{row('var(--card-wide-width)', '16 / 9', true)}{row('var(--card-poster-width)', '2 / 3', false)}</div>;
}

/** A grid loading: posters at their real size with their two caption lines, in the grid's own columns. */
function GridSkeleton() {
  return <div aria-hidden="true" className={styles.skeletonGrid}>{Array.from({length: 21}, (_, index) => <div key={index} className={styles.skeletonCell}>
    <Skeleton quiet sheen={index === 0} style={{aspectRatio: '2 / 3', width: '100%'}} /><Skeleton quiet line width="80%" /><Skeleton quiet line small width="40%" /></div>)}</div>;
}

/** What a screen may say about Home's hero: what to feature and how to draw its art, thumbnails and buttons. Never a size. */
export type HomeHeroProps<T extends HeroCandidate> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible'
  | 'renderArtwork' | 'renderThumbnail' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'titleLink' | 'onArtworkError' | 'onArtworkReady'>;
/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade and drift unchanged) at the rule's
 * fixed sizes. From tablet width its pager is the thumbnails with their names (mock.html home). */
export function HomeHero<T extends HeroCandidate>(props: HomeHeroProps<T>) {
  return <Hero {...props} moderate platform={usePhone() ? 'phone-web' : 'desktop'} />;
}
