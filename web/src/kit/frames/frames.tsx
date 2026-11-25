import type {ReactNode, RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {overlayForm, popoverWidth, type OverlayTask} from '@design';
import {AuthError, AuthKicker, AuthStage} from '../accounts';
import {Button} from '../controls';
import type {IconName} from '../Icon';
import {BottomSheet, Dialog, Popover, SidePanel, useFrameForm, type OverlayChange} from '../overlays';
import {StateLayout, type StateAction, type StateDetails, type StateRetry, type StateTone} from '../states';
import {SettingsNavLead} from '../shell/SettingsSurfaces';
import {Icon} from '../Icon';
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
export function PageHeader({title, back, trailing}: PageHeaderProps) {
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
/** Under a frame's header (a library's title and tabs, a page's title) the state fills the room the frame leaves and its words
 * start a header gap below the header; at the whole page's 170 from the top they were cut off under a library header. */
const pageStateRegion = {flex: '1 1 auto', height: 'auto'} as const;
const pageStateContent = {top: 'var(--frame-header-gap)', transform: 'none', maxHeight: 'calc(100% - 2 * var(--frame-header-gap))'} as const;
/** Frame 9: the whole page can't load, or is waiting the first time. Once per screen, in place of the page. */
export function PageStateFrame({primaryAction, secondaryAction, downloads, belowHeader = false, ...state}: PageStateProps & Readonly<{belowHeader?: boolean}>) {
  return <StateLayout {...state} primary={primaryAction} secondary={secondaryAction} style={belowHeader ? pageStateRegion : undefined} contentStyle={belowHeader ? pageStateContent : undefined}>{downloads}</StateLayout>;
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
  const form = overlayForm(task, useFrameForm()), {bar} = form, t = defaultI18n.t;
  const cancel = <Button onClick={() => onOpenChange(false, 'close-button')}>{t('next.overlays.cancel')}</Button>;
  const barAction = action && <Button variant="text" size="small" className={styles.barAction} disabled={action.disabled} onClick={action.onPress}>{action.label}</Button>;
  // Where the bar has no place for the action, it is the foot's primary, after Cancel.
  // A bar that already holds Cancel leaves the foot to the action alone (full width on a phone).
  const footer = foot ?? (action && bar.trailing !== 'action' ? <>{bar.leading !== 'cancel' && cancel}<Button variant="primary" disabled={action.disabled} onClick={action.onPress}>{action.label}</Button></> : undefined);
  const about = bar.subtitle ? subtitle : undefined;
  if (form.kind === 'sheet') return <BottomSheet open={open} onOpenChange={onOpenChange} title={title} subtitle={about} fitContent dialog
    leading={bar.leading === 'cancel' ? <Button variant="text" size="small" className={styles.barCancel} onClick={() => onOpenChange(false, 'close-button')}>{t('next.overlays.cancel')}</Button> : undefined}
    headerAction={bar.trailing === 'action' ? barAction : undefined} showCloseButton={bar.trailing === 'done'} footer={footer}>{children}</BottomSheet>;
  if (form.kind === 'popover' && anchorRef) return <Popover open={open} onOpenChange={onOpenChange} anchorRef={anchorRef} modal={form.modal} placement="bottom-start" title={title} width={popoverWidth.min.web} className={styles.taskPopover}
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
  /** A page pushed from another page with its own header band (a library's artwork band from Libraries, owner-libraries 2):
   * it takes the title's place from 600, and Back stays above it beside the list. */
  header?: ReactNode;
  /** Sections of rows (`SectionCard`): the one flexible region. */
  children?: ReactNode;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
}>;
/** Frame 4, one settings or owner page, or a form. At phone width its header is the compact bar ("‹ Settings · title ·
 * action"); from 600 the page title in the column beside the list, with the intro at most 720 wide. A page shown alone
 * (not beside the list) keeps Back above its title, the gutter and the page's column limit (tablets SC-8). */
export function SettingsPageFrame({title, back, action, status, actions, server, intro, tabs, header, children, state}: SettingsPageFrameProps) {
  const t = defaultI18n.t;
  return <div className={`${styles.frame} ${styles.page}`} data-pushed={header ? true : undefined}>
    <div className={styles.pageBar}><SheetBar title={title} back={back} action={action} /></div>
    {/* The server strip leads an owner page, over its title (final3-desktop-09; phone-app 19's card under the bar). */}
    {back && <div className={styles.pageBack}><Back back={back} /></div>}
    {server && !state && <div className={styles.pageServer}>{server}</div>}
    {header ? <div className={styles.pageOwnHead}>{header}</div> : <header className={styles.pageHead}><h1 className={styles.pageTitle}>{title}</h1>
      <span role="status" className={styles.pageStatus}>{status === 'saving' ? t('next.settings.saving') : status === 'saved' ? <><Icon name="i-check" size={16} />{t('next.settings.saved')}</> : null}</span>
      {actions && <span className={styles.pageActions}>{actions}</span>}</header>}
    {state ? <PageStateFrame {...state} /> : <>{intro !== undefined && <p className={styles.intro}>{intro}</p>}{tabs && <div className={styles.pageTabs}>{tabs}</div>}
      {/* The flexible region is a column at the card inset: whatever a page puts there (sections, rows, a page's own lists)
          lines up with the cards, on every width. */}
      <div className={styles.pageBody}>{children}</div></>}
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
