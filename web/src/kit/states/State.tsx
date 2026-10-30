import {useId, useLayoutEffect, useRef, type CSSProperties, type ReactNode, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Icon, type IconName} from '../Icon';
import {Button} from '../controls/Button';
import {Artwork, ColourBackdrop, type ArtworkSource} from '../artwork';
import {SidePanel} from '../overlays/SidePanel';
import {knownProgress} from './model';
import styles from './states.module.css';

export type StateTone = 'neutral' | 'warning' | 'error' | 'accent' | 'healthy';
export type StateAction = Readonly<{
  id: string;
  label: ReactNode;
  icon?: IconName;
  iconPosition?: 'start' | 'end';
  onPress: () => void;
  accessibilityLabel?: string;
  disabled?: boolean;
  loading?: boolean;
}>;
export type StateRetry = Readonly<{
  /** Actual remaining fraction from the retry controller; null when unknown. */
  progress: number | null;
  label: ReactNode;
  detail?: ReactNode;
}>;

export type CountdownRingProps = Readonly<{
  progress: number | null;
  size?: number;
  accessibilityLabel?: string;
  className?: string;
}>;
export function CountdownRing({progress, size = 24, accessibilityLabel, className}: CountdownRingProps) {
  const value = knownProgress(progress);
  return <svg className={[styles.countdown, className].filter(Boolean).join(' ')} width={size} height={size} viewBox="0 0 24 24"
    role={accessibilityLabel ? 'progressbar' : undefined} aria-label={accessibilityLabel}
    aria-hidden={accessibilityLabel ? undefined : true} aria-valuemin={accessibilityLabel ? 0 : undefined}
    aria-valuemax={accessibilityLabel ? 100 : undefined} aria-valuenow={accessibilityLabel && value !== null ? Math.round(value * 100) : undefined}
    data-progress={value === null ? 'unknown' : value}>
    <circle className={styles.ringTrack} cx={12} cy={12} r={9}/>
    {value !== null && value > 0 && <circle className={styles.ringArc} cx={12} cy={12} r={9} pathLength={1} strokeDasharray={1} strokeDashoffset={1 - value}/>}
  </svg>;
}

export type DetailsFact = Readonly<{id: string; label: ReactNode; value?: ReactNode; status?: Readonly<{label: ReactNode; tone?: StateTone}>}>;
export type OwnerDiagnostics = Readonly<{
  ownerMark: ReactNode;
  ownerTag: ReactNode;
  heading?: ReactNode;
  paragraphs?: readonly ReactNode[];
  codes?: readonly string[];
  time?: ReactNode;
  actions?: readonly StateAction[];
}>;
export type DetailsPanelProps = Readonly<{
  facts: readonly DetailsFact[];
  compact?: boolean;
  className?: string;
  id?: string;
}> & (Readonly<{audience: 'viewer'; owner?: never}> | Readonly<{audience: 'owner'; owner?: OwnerDiagnostics}>);

export function DetailsPanel(props: DetailsPanelProps) {
  const {facts, compact = false, className, id} = props;
  const owner = props.audience === 'owner' ? props.owner : undefined;
  return <div id={id} className={[styles.details, compact && styles.detailsCompact, className].filter(Boolean).join(' ')}>
    {facts.length > 0 && <dl className={styles.facts}>{facts.map(fact => <div key={fact.id} className={styles.fact} data-value={fact.value != null || undefined}>
      <dt>{fact.label}</dt>{fact.value != null && <dd>{fact.value}</dd>}
      {fact.status != null && <dd className={styles.factStatus} data-tone={fact.status.tone}>{fact.status.label}</dd>}
    </div>)}</dl>}
    {owner && <section className={styles.owner} data-owner-diagnostics>
      <div className={styles.ownerHeading}>{owner.ownerMark}
        <strong>{owner.heading ?? defaultI18n.t('next.states.ownerHeading')}</strong><span className={styles.ownerTagSlot}>{owner.ownerTag}</span></div>
      {owner.paragraphs?.map((paragraph, index) => <p key={index}>{paragraph}</p>)}
      {!!owner.codes?.length && <p className={styles.diagnosticCodes}>{defaultI18n.t('next.states.code', {count: owner.codes.length})}
        {owner.codes.map(code => <code key={code}>{code}</code>)}{owner.time && <span>{owner.time}</span>}</p>}
      {!!owner.actions?.length && <div className={styles.ownerActions}>{owner.actions.map(action => <Action key={action.id} action={action} variant="text"/>)}</div>}
    </section>}
  </div>;
}

export type StateStep = Readonly<{id: string; label: ReactNode; status: 'pending' | 'current' | 'done'; detail?: ReactNode; progress?: number | null}>;
export function StateSteps({steps}: {steps: readonly StateStep[]}) {
  return <ol className={styles.steps}>{steps.map(step => <li key={step.id} data-status={step.status} aria-current={step.status === 'current' ? 'step' : undefined}>
    <span className={styles.stepMark} aria-hidden="true">{step.status === 'done' ? <Icon name="i-check" size={16}/> : step.status === 'current' ? <CountdownRing progress={step.progress ?? null} size={26}/> : null}</span>
    <span>{step.label}</span>{step.detail && <small>{step.detail}</small>}
  </li>)}</ol>;
}

export type StateDetails = Readonly<{open: boolean; onOpenChange: (open: boolean) => void; content: ReactNode; label?: ReactNode}>;
export type StateContentProps = Readonly<{
  /** Catalogue or presentError output only; never an Error or server message. */
  title: ReactNode;
  body?: ReactNode;
  icon?: IconName;
  tone?: StateTone;
  eyebrow?: ReactNode;
  retry?: StateRetry;
  alive?: ReactNode;
  primary?: StateAction;
  secondary?: StateAction;
  links?: readonly StateAction[];
  details?: StateDetails;
  help?: ReactNode;
  afterActions?: ReactNode;
  children?: ReactNode;
}>;

function Action({action, variant, compact}: {action: StateAction; variant: 'primary' | 'glass' | 'text'; compact?: boolean}) {
  return <Button variant={variant} size={compact ? 'small' : 'hero'} icon={action.iconPosition === 'end' ? undefined : action.icon} disabled={action.disabled} loading={action.loading} aria-label={action.accessibilityLabel} onClick={action.onPress}>{action.iconPosition === 'end' && action.icon ? <span className={styles.trailingAction}>{action.label}<Icon name={action.icon} size={18}/></span> : action.label}</Button>;
}
function StateContent({title, body, icon, tone = 'neutral', eyebrow, retry, alive, primary, secondary, links, details, help, afterActions, children, compact = false, panelDetails = false, detailsTriggerRef}: StateContentProps & {compact?: boolean; panelDetails?: boolean; detailsTriggerRef?: RefObject<HTMLButtonElement | null>}) {
  const titleId = useId();
  const detailsId = useId();
  return <section className={styles.content} aria-labelledby={titleId} data-tone={tone}>
    {eyebrow && <div className={styles.eyebrow}>{eyebrow}</div>}
    {icon && <div className={styles.stateIcon}><Icon name={icon} size={compact ? 22 : 28}/></div>}
    <h1 id={titleId}>{title}</h1>
    {body && <p className={styles.body}>{body}</p>}
    {retry && <div className={styles.alive} aria-live="polite"><CountdownRing progress={retry.progress}/><strong>{retry.label}</strong>{retry.detail && <span className={styles.retryDetail}>{retry.detail}</span>}</div>}
    {alive && <div className={styles.alive}>{alive}</div>}
    {children}
    {(primary || secondary || links?.length || details) && <div className={styles.actions}>
      {primary && <Action action={primary} variant="primary" compact={compact}/>}
      {secondary && <Action action={secondary} variant="glass" compact={compact}/>}
      {links?.map(action => <Action key={action.id} action={action} variant="text" compact={compact}/>)}
      {details && <button ref={detailsTriggerRef} hidden={panelDetails && details.open} type="button" className={styles.detailsToggle} aria-controls={panelDetails ? undefined : detailsId} aria-expanded={details.open} onClick={() => details.onOpenChange(!details.open)}>
        {details.label ?? defaultI18n.t('next.states.details')}<Icon name={panelDetails ? 'i-right' : details.open ? 'i-up' : 'i-down'} size={panelDetails ? 16 : 18}/>
      </button>}
    </div>}
    {!panelDetails && details?.open && <div id={detailsId}>{details.content}</div>}
    {afterActions}
    {help && <div className={styles.help}>{help}</div>}
  </section>;
}

/** `shape` and `fill` are retired: every colour state shows the one standard backdrop. */
export type StateBackdrop = Readonly<{kind: 'colour'; /** @deprecated */ shape?: 'side' | 'round' | 'top'; /** @deprecated */ fill?: boolean}> | Readonly<{kind: 'frozen'; source: ArtworkSource}>;
export type StateLayoutProps = StateContentProps & Readonly<{
  backdrop?: StateBackdrop | null;
  variant?: 'page' | 'player' | 'landscape';
  /** Screen/shell owns navigation and player controls. */
  chrome?: ReactNode;
  after?: ReactNode;
  className?: string;
  contentStyle?: CSSProperties;
  style?: CSSProperties;
}>;
export function StateLayout({backdrop = {kind: 'colour'}, variant = 'page', chrome, after, className, contentStyle, style, ...content}: StateLayoutProps) {
  const detailsTrigger = useRef<HTMLButtonElement>(null);
  const panel = variant === 'landscape' ? content.details : undefined;
  const panelWasOpen = useRef(false);
  useLayoutEffect(() => {
    // The trigger is hidden while this compact panel is open. Restore after
    // its DOM has become visible, rather than during the panel's cleanup.
    if (panelWasOpen.current && !panel?.open) detailsTrigger.current?.focus({preventScroll: true});
    panelWasOpen.current = !!panel?.open;
  }, [panel?.open]);
  const positionStyle = {...contentStyle, ...(typeof contentStyle?.top === 'number' ? {'--state-content-top': `${contentStyle.top}px`} : {})} as CSSProperties;
  return <div className={[styles.layout, styles[variant], className].filter(Boolean).join(' ')} style={style} data-details-open={panel?.open || undefined}>
    {/* A page state is transparent over the window's standard backdrop, wherever it sits; a player
        state covers the picture with the same backdrop. */}
    {backdrop?.kind === 'colour' ? variant === 'page' ? null : <ColourBackdrop/> : backdrop && <div className={styles.frozen} aria-hidden="true"><Artwork source={backdrop.source} alt=""/><div className={styles.frozenShade}/></div>}
    {chrome}
    <div className={styles.position} style={positionStyle}><StateContent {...content} compact={variant === 'landscape'} panelDetails={!!panel} detailsTriggerRef={detailsTrigger}/></div>
    {panel && <SidePanel open={panel.open} onOpenChange={panel.onOpenChange} title={panel.label ?? defaultI18n.t('next.states.details')} width={352} topInset={10} className={styles.landscapeDetails} returnFocusRef={detailsTrigger}>{panel.content}</SidePanel>}
    {after}
  </div>;
}

export type StateViewProps = StateContentProps & Readonly<{variant?: 'inline' | 'scan' | 'search' | 'compact' | 'page'; className?: string; style?: CSSProperties}>;
export function StateView({variant = 'inline', className, style, ...content}: StateViewProps) {
  return <div className={[styles.inline, styles[variant], className].filter(Boolean).join(' ')} style={style} role={variant === 'scan' ? 'status' : undefined}>
    {variant === 'scan' && <div className={styles.ghosts} aria-hidden="true"><span/><span/><span/></div>}
    <StateContent {...content} compact={variant === 'compact'}/>
  </div>;
}
