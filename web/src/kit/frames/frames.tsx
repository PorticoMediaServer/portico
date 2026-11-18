import type {ReactNode, RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {overlayForm, type OverlayTask} from '@design';
import {AuthError, AuthKicker, AuthStage} from '../accounts';
import type {ArtworkSource} from '../artwork';
import {Button, ResumeButton, Tabs, type ChoiceItem} from '../controls';
import type {IconName} from '../Icon';
import {Hero, type HeroCandidate, type HeroProps} from '../layout';
import {BottomSheet, Dialog, Popover, SidePanel, useFrameForm, type OverlayChange} from '../overlays';
import {usePhone} from '../overlays/shared';
import {Skeleton, StateLayout, type StateAction, type StateDetails, type StateRetry, type StateTone} from '../states';
import {SettingsNavLead} from '../shell/SettingsSurfaces';
import {Icon} from '../Icon';
import {ArtBox} from './parts';
import styles from './frames.module.css';

/** The screen frames on the web (design/ui-v1 `system-frames.html`), with the native frames' names and props.
 * A screen picks one and fills its slots; the frame owns the header, gutters, rhythm and page-level states, the
 * shell owns the nav and tab bar around it, and the window (kit/backdrop.css) owns the backdrop. FRAMES.md is the brief. */

export type FrameBack = Readonly<{label: string; onPress: () => void}>;
function Back({back}: {back: FrameBack}) {
  return <Button variant="text" size="small" icon="i-left" className={styles.back} onClick={back.onPress}>{back.label}</Button>;
}

export type PageHeaderProps = Readonly<{
  title: string;
  /** A page that was pushed: Back on its own line, named after the page it returns to. */
  back?: FrameBack;
  /** Header actions (Filters, sort), beside the title. */
  trailing?: ReactNode;
}>;
/** Rule 3, the one header of a page that is not a title page and not in a sheet. The title wraps and is never cut. */
function PageHeader({title, back, trailing}: PageHeaderProps) {
  return <header className={styles.header} data-back={back ? true : undefined}>{back && <Back back={back} />}<div className={styles.headerRow}><h1 className={styles.pageTitle}>{title}</h1>{trailing}</div></header>;
}

export type PageStateProps = Readonly<{
  icon: IconName; tone?: StateTone; title: string;
  /** One sentence: the flexible region. */
  body?: string;
  primaryAction?: StateAction; secondaryAction?: StateAction;
  retry?: StateRetry;
  /** The owner-only Details block. */
  details?: StateDetails;
  /** What still works: "Your downloads still play" (states 4a), between the retry line and the buttons. */
  downloads?: ReactNode;
  /** One paragraph under the buttons ("If the server is asleep…", states 4a, 5). */
  help?: ReactNode;
}>;
/** Frame 9: the whole page can't load, or is waiting the first time. Once per screen, in place of the page. */
export function PageStateFrame({primaryAction, secondaryAction, downloads, ...state}: PageStateProps) {
  return <StateLayout {...state} primary={primaryAction} secondary={secondaryAction}>{downloads}</StateLayout>;
}

export type BrowseFrameProps = Readonly<{
  /** The rows or the grid: the one flexible region. Each child is a kit `Shelf` (it draws its own heading) or a grid;
   * the frame spaces them. A row with no items is left out whole. */
  children?: ReactNode;
  /** The whole page is loading for the first time, or can't load: drawn in place of the rows. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}> & (
  /** A destination or a pushed list: its title is the header. */
  | Readonly<{header: PageHeaderProps; hero?: never}>
  /** Home: the hero leads, under the shell's nav. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, Live TV, Saved, Search results. */
export function BrowseFrame({header, hero, state, children}: BrowseFrameProps) {
  const failure = state && 'title' in state ? state : undefined;
  return <div className={`${styles.frame} ${styles.browse}`} data-hero={hero ? true : undefined}>
    {header ? <PageHeader {...header} /> : hero ?? <div className={styles.browseTop} />}
    {failure ? <PageStateFrame {...failure} /> : <div className={styles.browseRows}>{state ? <RowsSkeleton /> : children}</div>}
  </div>;
}
/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const row = (width: string, ratio: string, sheen: boolean) => <div className={styles.skeletonRow}><Skeleton quiet line width="var(--card-poster-width)" />
    <div className={styles.skeletonCards}>{Array.from({length: 9}, (_, index) => <Skeleton quiet key={index} width={width} sheen={sheen && index === 0} style={{aspectRatio: ratio}} />)}</div></div>;
  return <div aria-hidden="true" className={styles.browseRows}>{row('var(--card-wide-width)', '16 / 9', true)}{row('var(--card-poster-width)', '2 / 3', false)}</div>;
}

/** What a screen may say about Home's hero: what to feature and how to draw its art and buttons. Never a size. */
export type HomeHeroProps<T extends HeroCandidate> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible'
  | 'renderArtwork' | 'renderThumbnail' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'titleLink' | 'onArtworkError' | 'onArtworkReady'>;
/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade and drift unchanged) at the rule's fixed sizes. */
export function HomeHero<T extends HeroCandidate>(props: HomeHeroProps<T>) {
  return <Hero {...props} moderate platform={usePhone() ? 'phone-web' : 'desktop'} />;
}

export type TitleAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean;
  /** Shown with its label where there is room (from tablet width): Start over, My List. A round button on a phone. */
  labelled?: boolean}>;
/** The one flexible button. With `progress` it is Resume. From tablet width the time left is beside its label, the
 * played part runs along its lower edge and Start over is the button after it (detail-desktop 2). On a phone the
 * played part, the time left and Start over are one line under the action row (detail-phone 3). */
export type TitlePrimary = Omit<TitleAction, 'labelled'> & Readonly<{progress?: number; remaining?: string; startOver?: Readonly<{label: string; onPress: () => void}>}>;
/** One part of the credits line: "Directed by" and the names. */
export type TitleCredit = Readonly<{label: string; names: string}>;
export type TitleFrameProps = Readonly<{
  /** The picture, as the kit's `AmbientArt` with `treatment="title"`; it fades itself into the backdrop. */
  art: ReactNode;
  /** Phone width only: the glass circle over the art. Wider, the nav is the way back. */
  back: FrameBack;
  topAction?: TitleAction;
  title: string;
  /** The metadata line (`HeroFacts`). Its line is kept when there is nothing to show. */
  metadata?: ReactNode;
  /** The one flexible button: at phone width it takes the room the round buttons leave. */
  primary: TitlePrimary;
  /** The buttons after it, in order: labelled ones first (Start over, My List), then round ones (watched, More). One
   * that the title doesn't support stays in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** One bold line over the synopsis. Flexible: collapses when absent. */
  tagline?: string;
  /** Flexible: collapses when absent. */
  synopsis?: string;
  /** "Directed by" and "Starring", one line under the synopsis from tablet width. Flexible: collapses when absent. */
  credits?: readonly TitleCredit[];
  tabs?: Readonly<{items: readonly ChoiceItem[]; value: string; onValueChange: (value: string) => void}>;
  /** The tab panel: the one flexible region. */
  children?: ReactNode;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}>;

/** Frame 2, a title page. Fixed: the art box, Back, the title slot (the title sits on its bottom edge and grows
 * upward over the art), the metadata line, the action row and the tabs bar. Flexible: the synopsis and the tab panel. */
export function TitleFrame({art, back, topAction, title, metadata, primary, actions = [], tagline, synopsis, credits, tabs, children, state}: TitleFrameProps) {
  const phone = usePhone(), failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  const glass = (action: TitleAction) => <Button variant="glass" size="small" iconOnly icon={action.icon} aria-label={action.label} disabled={action.disabled} onClick={action.onPress} />;
  const backButton = glass({icon: 'i-left', label: back.label, onPress: back.onPress});
  if (failure) return <div className={styles.frame}>{phone && <div className={styles.titleTop}>{backButton}</div>}<PageStateFrame {...failure} /></div>;
  return <div className={`${styles.frame} ${styles.title}`}>
    <div className={styles.titleHead}>
      <div className={styles.titleArt}>{art}</div>
      <div className={styles.titleTop}>{backButton}{topAction && glass(topAction)}</div>
      <div className={styles.titleCopy}>
        {loading ? <Skeleton width="70%" height="var(--frame-hero-title-lockup)" /> : <h1 className={styles.titleLockup}>{title}</h1>}
        <div className={styles.titleMeta}>{loading ? <Skeleton line small width="40%" /> : metadata}</div>
      </div>
      <div className={styles.titleBody}>
        <div className={styles.titleActions}>
          {primary.progress !== undefined && !phone
            ? <ResumeButton icon={primary.icon} progress={primary.progress} remaining={primary.remaining} disabled={primary.disabled || loading} onClick={primary.onPress}>{primary.label}</ResumeButton>
            : <Button variant="primary" phone={phone} icon={primary.icon} disabled={primary.disabled || loading} onClick={primary.onPress}>{primary.label}</Button>}
          {primary.startOver && !phone && <Button variant="glass" icon="i-restart" disabled={loading} onClick={primary.startOver.onPress}>{primary.startOver.label}</Button>}
          {actions.map(action => action.labelled && !phone
            ? <Button key={action.label} variant="glass" icon={action.icon} disabled={action.disabled || loading} onClick={action.onPress}>{action.label}</Button>
            : <Button key={action.label} variant="glass" phone={phone} iconOnly icon={action.icon} aria-label={action.label} title={action.label} disabled={action.disabled || loading} onClick={action.onPress} className={styles.titleRound} />)}
        </div>
        {primary.progress !== undefined && phone && !loading && <div className={styles.titleProgress}>
          <span role="progressbar" aria-valuemin={0} aria-valuemax={1} aria-valuenow={primary.progress} aria-label={primary.remaining}><i style={{width: `${primary.progress * 100}%`}} /></span>
          <b className={styles.rowHelp}>{primary.remaining}</b>
          {primary.startOver && <Button variant="text" size="xs" icon="i-restart" onClick={primary.startOver.onPress}>{primary.startOver.label}</Button>}
        </div>}
        {!loading && (tagline !== undefined || synopsis !== undefined || credits?.length) && <div className={styles.titleWords}>
          {tagline !== undefined && <p className={styles.rowLabel}>{tagline}</p>}
          {synopsis !== undefined && <p className={`${styles.intro} ${styles.synopsis}`}>{synopsis}</p>}
          {credits && credits.length > 0 && <p className={`${styles.rowHelp} ${styles.titleCredits}`}>{credits.map(credit => <span key={credit.label}>{credit.label} <b>{credit.names}</b></span>)}</p>}
        </div>}
      </div>
    </div>
    {tabs && <Tabs className={styles.titleTabs} listClassName={styles.tabsInset} phone={phone} items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} />}
    <div className={styles.titlePanel}>{loading ? null : children}</div>
  </div>;
}

export type SheetBarProps = Readonly<{
  title: string;
  /** Named after the page it returns to. */
  back?: FrameBack;
  /** The bar's one text action (Done, Save). */
  action?: Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
}>;
/** Rule 3, the compact bar, 56 high: Back, the title centred at 17/700, one text action. */
function SheetBar({title, back, action}: SheetBarProps) {
  return <header className={styles.sheetBar}>{back ? <Back back={back} /> : <span />}<h1 className={styles.barTitle}>{title}</h1>
    {action ? <Button variant="text" size="small" className={styles.barAction} disabled={action.disabled} onClick={action.onPress}>{action.label}</Button> : <span />}</header>;
}

export type SheetAction = Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
export type SheetFrameProps = Readonly<{
  open: boolean; onOpenChange: OverlayChange;
  /** R9: what the overlay is for. Its kind on this form (sheet, dialog, side panel, popover), what its bar holds and how
   * its foot lays out come from packages/design `overlayForms`; a screen never picks the kind. */
  task: Exclude<OverlayTask, 'settings' | 'account' | 'notifications'>;
  title: string;
  /** The item it is about ("Dune: Part Two"), where the bar has a place for it. */
  subtitle?: string;
  /** A task's own action: in the bar where the form puts it there (a phone's task sheet), else the foot's primary after Cancel. */
  action?: SheetAction;
  /** The rows or a short form: the flexible region. It grows to the surface's limit, then scrolls. */
  children: ReactNode;
  /** Foot buttons (Back and Add source; Cancel and Delete), laid out by the form's foot: equal halves, full width or at the end. */
  foot?: ReactNode;
  /** A popover's trigger. It opens below it and the trigger stays in view. */
  anchorRef?: RefObject<HTMLElement | null>;
}>;
/** Frame 5 and rule 2, by R9: menus, pickers, confirmations, short tasks, details and filters. A plain raised surface
 * with its corners, grab and scrim; it takes no background or header of the content's own. */
export function SheetFrame({open, onOpenChange, task, title, subtitle, action, children, foot, anchorRef}: SheetFrameProps) {
  const form = overlayForm(task, useFrameForm())!, {bar} = form, t = defaultI18n.t;
  const cancel = <Button onClick={() => onOpenChange(false, 'close-button')}>{t('next.overlays.cancel')}</Button>;
  const barAction = action && <Button variant="text" size="small" className={styles.barAction} disabled={action.disabled} onClick={action.onPress}>{action.label}</Button>;
  // Where the bar has no place for the action, it is the foot's primary, after Cancel.
  // A bar that already holds Cancel leaves the foot to the action alone (full width on a phone).
  const footer = foot ?? (action && bar.trailing !== 'action' ? <>{bar.leading !== 'cancel' && cancel}<Button variant="primary" disabled={action.disabled} onClick={action.onPress}>{action.label}</Button></> : undefined);
  const about = bar.subtitle ? subtitle : undefined;
  if (form.kind === 'sheet') return <BottomSheet open={open} onOpenChange={onOpenChange} title={title} subtitle={about} fitContent dialog
    leading={bar.leading === 'cancel' ? <Button variant="text" size="small" className={styles.barCancel} onClick={() => onOpenChange(false, 'close-button')}>{t('next.overlays.cancel')}</Button> : undefined}
    headerAction={bar.trailing === 'action' ? barAction : undefined} showCloseButton={bar.trailing === 'done'} footer={footer}>{children}</BottomSheet>;
  if (form.kind === 'popover' && anchorRef) return <Popover open={open} onOpenChange={onOpenChange} anchorRef={anchorRef} modal={form.modal} placement="bottom-start" title={title} width={340}
    description={bar.title === 'start' ? <span className={styles.popoverHead}><b>{title}</b>{about && <span>{about}</span>}</span> : undefined}>{children}</Popover>;
  if (form.kind === 'side' && !form.modal) return <SidePanel open={open} onOpenChange={onOpenChange} title={title} description={about} footer={footer} topInset={0} width={440}>{children}</SidePanel>;
  return <Dialog open={open} onOpenChange={onOpenChange} title={title} description={about} responsive={false} placement={form.kind === 'side' ? 'right' : 'center'} width={form.kind === 'side' ? 440 : undefined}
    showCloseButton={bar.trailing === 'close'} foot={form.foot} footer={footer}>{children}</Dialog>;
}

export type SettingsListFrameProps = Readonly<{
  /** The bar over the list at phone width (Settings, Done). */
  bar: SheetBarProps;
  /** Phone width: the profile card (avatar, name, "Signed in to … as …") under the search field. */
  profile?: ReactNode;
  /** Phone width: the four quick tiles (Switch profile, Switch server, Notifications with its count, Watch Together). */
  shortcuts?: ReactNode;
  /** The list: search, the Account groups, then the Server band. */
  list: ReactNode;
  /** The selected page, a `SettingsPageFrame`. At phone width it takes the list's place. */
  page?: ReactNode;
}>;
/** Frame 3, the index of Settings. From 600: the list in its own sticky column beside the page. At phone width: the
 * list under its compact bar, or the selected page in its place. */
export function SettingsListFrame({bar, profile, shortcuts, list, page}: SettingsListFrameProps) {
  const lead = useFrameForm() === 'phone' && (profile || shortcuts) ? <>{profile}{shortcuts}</> : null;
  return <div className={`${styles.frame} ${styles.settingsFrame}`}>
    <div className={styles.settingsMaster} data-covered={page ? true : undefined}><div className={styles.settingsBar}><SheetBar {...bar} /></div><SettingsNavLead.Provider value={lead}>{list}</SettingsNavLead.Provider></div>
    <div className={styles.settingsDetail}>{page}</div>
  </div>;
}

export type SettingsPageFrameProps = Readonly<{
  title: string;
  /** Back to the list, at phone width; named after the page it returns to. */
  back?: FrameBack;
  /** The compact bar's one text action, at phone width (Done, Save). */
  action?: SheetBarProps['action'];
  /** Beside the title from 600: "Saving…" or "✓ Saved". */
  status?: 'saving' | 'saved';
  /** Beside the title from 600: the page's own buttons ("+ Add library", "Invite someone", "Add account"). */
  actions?: ReactNode;
  /** An owner page's server strip: the kit `ServerHeader` (desktop strip, phone card), first under the header. */
  server?: ReactNode;
  /** One short paragraph under the header. */
  intro?: string;
  /** The page's own tabs (People: Accounts, Invitations, Devices), the kit `Tabs`, under the intro. */
  tabs?: ReactNode;
  /** Sections of rows (`SectionCard`): the one flexible region. */
  children?: ReactNode;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
}>;
/** Frame 4, one settings or owner page, or a form. At phone width its header is the compact bar ("‹ Settings · title ·
 * action"); from 600 the page title in the column beside the list, with the intro at most 720 wide. A page shown alone
 * (not beside the list) keeps Back above its title, the gutter and the page's column limit (tablets SC-8). */
export function SettingsPageFrame({title, back, action, status, actions, server, intro, tabs, children, state}: SettingsPageFrameProps) {
  const t = defaultI18n.t;
  return <div className={`${styles.frame} ${styles.page}`}>
    <div className={styles.pageBar}><SheetBar title={title} back={back} action={action} /></div>
    {/* The server strip leads an owner page, over its title (final3-desktop-09; phone-app 19's card under the bar). */}
    {back && <div className={styles.pageBack}><Back back={back} /></div>}
    {server && !state && <div className={styles.pageServer}>{server}</div>}
    <header className={styles.pageHead}><h1 className={styles.pageTitle}>{title}</h1>
      <span role="status" className={styles.pageStatus}>{status === 'saving' ? t('next.settings.saving') : status === 'saved' ? <><Icon name="i-check" size={16} />{t('next.settings.saved')}</> : null}</span>
      {actions && <span className={styles.pageActions}>{actions}</span>}</header>
    {state ? <PageStateFrame {...state} /> : <>{intro !== undefined && <p className={styles.intro}>{intro}</p>}{tabs && <div className={styles.pageTabs}>{tabs}</div>}{children}</>}
  </div>;
}

export type SignInFrameProps = Readonly<{
  /** A page after the first has Back above its title. */
  back?: FrameBack;
  /** The accent line over the title ("Sign in to"), with the server's name as the title (auth-4, auth-6). */
  kicker?: string;
  title: string;
  /** One line under the title. */
  lede?: string;
  /** Said above the fields; the fields and the primary button under them keep their order. */
  error?: string;
  /** The form: fields, then the primary button, then lesser choices. The one flexible region. */
  children: ReactNode;
}>;
/** Frame 6, the pages before you're in. The kit's `AuthStage` owns the wordmark, the column and its inset, and
 * Terms and Privacy at the bottom; this names the slots a sign-in page fills. */
export function SignInFrame({back, kicker, title, lede, error, children}: SignInFrameProps) {
  return <AuthStage>{back && <Back back={back} />}{kicker !== undefined && <AuthKicker>{kicker}</AuthKicker>}<h1>{title}</h1>{lede !== undefined && <p>{lede}</p>}<div className={styles.signInForm}><AuthError message={error} />{children}</div></AuthStage>;
}
