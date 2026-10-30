import {useOverlayPortalTarget} from '../overlays/portal-target';
import {Fragment, useRef, type RefObject} from 'react';
import {Dialog} from 'radix-ui';
import {Icon} from '../Icon';
import type {MediaLayout} from './types';
import styles from './media.module.css';

export type PlaybackRoute = 'original' | 'repackaged' | 'converted';
export type PlaybackInfoField = 'container' | 'codec' | 'hdr' | 'bitDepth' | 'resolution' | 'frameRate' | 'screen' | 'format' | 'channels' | 'sampleRate' | 'output' | 'track' | 'shown';
/** Already localized from the genuine VS10 source/delivery report and actual output observations.
 * No private presentation, capability target or unstructured reason is an input to this view. */
export type PlaybackInfoProjection = Readonly<{
  itemTitle: string; deviceName?: string; route: PlaybackRoute;
  explanation?: string; networkAndBitrate?: string;
  sections: readonly Readonly<{
    kind: 'picture' | 'sound' | 'subtitles'; route: PlaybackRoute;
    rows: readonly Readonly<{field: PlaybackInfoField; source?: string; delivered?: string; changed: boolean}>[];
    losses: readonly string[];
  }>[];
}>;
/** Supplied by the catalogue adapter; only frame text, never raw reason codes/sentences. */
export type PlaybackInfoCopy = Readonly<{
  title: string; close: string; back: string; route: string; inFile: string; playingHere: string;
  routes: Readonly<Record<PlaybackRoute, string>>;
  sections: Readonly<Record<'picture' | 'sound' | 'subtitles', string>>;
  fields: Readonly<Record<PlaybackInfoField, string>>;
  routeName(route: string): string;
  originalValue(value: string): string;
}>;
export type PlaybackInfoProps = Readonly<{
  projection: PlaybackInfoProjection; copy: PlaybackInfoCopy; layout?: MediaLayout;
  open: boolean; onOpenChange(open: boolean): void;
  returnFocusRef: RefObject<HTMLElement | null>;
  /** The phone host supplies the actual picture edge. No guessed 16:9/window geometry. */
  phoneTop?: number;
}>;

function RouteStepper({route, copy}: {route: PlaybackRoute; copy: PlaybackInfoCopy}) {
  return <div className={styles.routeSteps} data-route={route} role="img" aria-label={copy.routeName(copy.routes[route])}>{(['original', 'repackaged', 'converted'] as const).map((value, i) => <Fragment key={value}>{i > 0 && <i />}<span data-current={value === route || undefined}>{copy.routes[value]}</span></Fragment>)}</div>;
}

/** An independent panel sibling. Opening it never replaces, pauses or remounts the media view. */
export function PlaybackInfo({projection, copy, layout = 'desktop', open, onOpenChange, returnFocusRef, phoneTop}: PlaybackInfoProps) {
  const portalTarget = useOverlayPortalTarget();
  const close = useRef<HTMLButtonElement>(null), blocks = useRef<(HTMLElement | null)[]>([]);
  return <Dialog.Root open={open} onOpenChange={onOpenChange} modal={layout === 'tv'}><Dialog.Portal container={portalTarget}>
    {layout === 'tv' && <Dialog.Overlay className={styles.signalScrim} />}
    <Dialog.Content className={styles.playbackInfo} data-layout={layout} style={layout === 'phone' && phoneTop !== undefined ? {top: phoneTop} : undefined}
      aria-describedby={undefined} onCloseAutoFocus={event => { event.preventDefault(); returnFocusRef.current?.focus({preventScroll: true}); }}
      onOpenAutoFocus={event => { if (layout === 'tv') { event.preventDefault(); blocks.current[0]?.focus(); } }}
      onKeyDown={event => {
        if (layout !== 'tv' || !['ArrowUp', 'ArrowDown'].includes(event.key)) return;
        const at = blocks.current.findIndex(block => block === document.activeElement);
        if (at < 0) return;
        event.preventDefault(); const next = blocks.current[Math.max(0, Math.min(blocks.current.length - 1, at + (event.key === 'ArrowDown' ? 1 : -1)))];
        next?.focus({preventScroll: true}); next?.scrollIntoView({block: 'nearest', behavior: 'instant'});
      }}>
      <header className={styles.infoHeading}><Dialog.Title>{copy.title}</Dialog.Title><button type="button" ref={close} className={styles.close} aria-label={copy.close} onClick={() => onOpenChange(false)}><Icon name="i-x" size={22} /></button></header>
      <p className={styles.infoIdentity}><b>{projection.itemTitle}</b>{projection.deviceName && <><i />{projection.deviceName}</>}</p>
      <section className={styles.infoRoute} aria-label={copy.route}><RouteStepper route={projection.route} copy={copy} />{projection.explanation && <p className={styles.infoReason}>{projection.explanation}</p>}{projection.networkAndBitrate && <p className={styles.infoNetwork}>{projection.networkAndBitrate}</p>}</section>
      {layout !== 'phone' && <div className={styles.compareHeading}><span /><span>{copy.inFile}</span><span>{copy.playingHere}</span></div>}
      <div className={styles.infoBody} onScroll={event => { event.currentTarget.dataset.scrolled = String(event.currentTarget.scrollTop > 0); }}>
        {projection.sections.map((section, index) => <section key={section.kind} className={styles.infoSection} tabIndex={layout === 'tv' ? 0 : undefined} ref={element => { blocks.current[index] = element; }}>
          <h3>{copy.sections[section.kind]}<span className={styles.routeBadge} data-route={section.route}>{copy.routes[section.route]}</span></h3>
          <dl className={styles.compare}>{section.rows.map(row => <Fragment key={row.field}><dt>{copy.fields[row.field]}</dt>
            {layout !== 'phone' && <dd>{row.source}</dd>}
            <dd data-changed={row.changed || undefined}>{row.delivered}{layout === 'phone' && row.changed && row.source && <small>{copy.originalValue(row.source)}</small>}</dd>
          </Fragment>)}</dl>
          {section.losses.map(loss => <p className={styles.lossLine} key={loss}><svg width={layout === 'tv' ? 24 : 16} height={layout === 'tv' ? 24 : 16} viewBox="0 0 24 24" aria-hidden="true"><g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><circle cx="12" cy="12" r="8.5" /><path d="m6 6 12 12" /></g></svg>{loss}</p>)}
        </section>)}
      </div>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>;
}
