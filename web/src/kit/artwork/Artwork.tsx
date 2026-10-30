import {useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties} from 'react';
import {observeArtwork, type ArtworkRead} from './source';
import {createArtworkDecodeLifetime, createArtworkPaintLifetime, createArtworkVisualLifetime} from './decode-lifetime';
import {createPaintAdmission} from './visual-admission';
import {observeVisibleArtwork} from './visible-artwork';
import type {ArtworkImage, ArtworkProps, ArtworkSource, ArtworkStatus} from './types';
import styles from './artwork.module.css';

export const artClass = (...names: (string | undefined | false)[]) => names.filter(Boolean).join(' ');
export function artSize({width, height, style}: {width?: number | string; height?: number | string; style?: CSSProperties}): CSSProperties {
  return {...(width === undefined ? {} : {width}), ...(height === undefined ? {} : {height}), ...style};
}

type ArtworkCallbacks = Pick<ArtworkProps, 'onReady' | 'onError' | 'onStatusChange'>;
type OriginalArtwork = {id: number; live: boolean; scope: object; source: ArtworkSource; callbacks: ArtworkCallbacks; reportedStatus?: ArtworkStatus};
type MountedArtwork = ArtworkRead & {lease?: OriginalArtwork};
let originalArtworkSequence = 0;
function useArtwork(source: ArtworkSource | null | undefined, visible: boolean, callbacks: ArtworkCallbacks): MountedArtwork {
  const scheduler = source?.scheduler, key = source?.key;
  // Runtime factories mint source wrappers during render. Their exact key and
  // retention owner, rather than a fresh equivalent closure, own this lease.
  // Custom sources still retire when their retain callback changes.
  const retention = source?.retainScope ?? source?.retain;
  const order = source?.request?.order, priority = source?.request?.priority, group = source?.request?.group, large = source?.request?.large;
  const scope = useMemo(() => ({scheduler, key, retention, visible, order, priority, group, large}), [scheduler, key, retention, visible, order, priority, group, large]);
  const original = useRef<{scope: typeof scope; source: typeof source; callbacks: ArtworkCallbacks} | undefined>(undefined);
  if (original.current?.scope !== scope) original.current = {scope, source, callbacks};
  const owner = useRef<OriginalArtwork | undefined>(undefined);
  const [read, setRead] = useState<MountedArtwork>({status: 'empty'});
  useLayoutEffect(() => {
    const captured = original.current;
    const {scheduler, key, visible, order, priority, group, large} = scope;
    if (captured?.scope !== scope || !scheduler || !key || !visible || !captured.source) return;
    // Capture callbacks with this original request. Later props cannot lend a
    // replacement callback or cache fact to its pending delivery/decode work.
    const lease: OriginalArtwork = {id: ++originalArtworkSequence, live: true, scope, source: captured.source, callbacks: captured.callbacks}; owner.current = lease;
    const stop = observeArtwork({scheduler, key, retain: captured.source.retain, request: {order, priority, group, large}}, value => {
      if (lease.live && owner.current === lease) setRead({...value, lease});
    });
    return () => {lease.live = false; if (owner.current === lease) owner.current = undefined; stop();};
  }, [scope]);
  const value: MountedArtwork = visible && scheduler && key
    ? read.lease?.live && read.lease.scope === scope && owner.current === read.lease ? read : {status: 'loading'}
    : {status: 'empty'};
  // Same resource lease as before, retired in layout before a successor paints.
  useLayoutEffect(() => value.image?.retain?.(), [value.image, value.lease]);
  return value;
}

type DecodeOwner = {live: boolean; decoder: ReturnType<typeof createArtworkDecodeLifetime<ArtworkImage>>};
type ImageMount = {id: number; image?: ArtworkImage; lease?: OriginalArtwork; owner?: DecodeOwner};
type VisualOwner = ReturnType<typeof createArtworkVisualLifetime<ArtworkImage, DecodeOwner>> & {lease: OriginalArtwork};
let imageMountSequence = 0;
// Only live intersecting mounts enter this registry. The helper prunes each
// scheduler/group when the last registration leaves.
const paintAdmission = createPaintAdmission();

export function Artwork({source, alt, fit = 'cover', position, zoom = 1, saturation = 1, brightness = 1, visible = true, arrivalDurationMs, fallback, className, style, width, height, onStatusChange, onReady, onError}: ArtworkProps) {
  const read = useArtwork(source, visible, {onStatusChange, onReady, onError});
  const element = useRef<HTMLImageElement>(null), container = useRef<HTMLSpanElement>(null), visualOwner = useRef<VisualOwner | undefined>(undefined);
  // A same-key new image or original request gets its own DOM node: a queued
  // event from the old node must not be handled as a new image's load/error.
  const mount = useMemo<ImageMount>(() => ({id: ++imageMountSequence, image: read.image, lease: read.lease}), [read.image, read.lease]);
  const currentMount = useRef(mount); currentMount.current = mount;
  const delivered = useRef<{image: typeof read.image; key: string; scheduler: ArtworkSource['scheduler']} | undefined>(undefined);
  const reportedError = useRef<{key: string; scheduler: ArtworkSource['scheduler']} | undefined>(undefined);
  const [decoded, setDecoded] = useState<{mount?: ImageMount; owner?: DecodeOwner; failed: boolean}>({failed: false});
  const currentDecode = !!read.image && decoded.mount === mount && decoded.owner === mount.owner && mount.owner?.live === true && read.lease?.live === true;
  const failed = currentDecode && decoded.failed;
  const ready = currentDecode && !decoded.failed;
  const status = failed ? 'error' : read.image ? ready ? 'ready' : 'loading' : read.status;
  const scheduler = source?.scheduler, key = source?.key;
  useLayoutEffect(() => {
    delivered.current = undefined; reportedError.current = undefined;
  }, [visible, scheduler, key]);
  useLayoutEffect(() => {
    const lease = read.lease, target = container.current;
    if (!lease?.live || !target) return;
    const owner: VisualOwner = {...createArtworkVisualLifetime<ArtworkImage, DecodeOwner>(() => paintAdmission.mount({
      scope: lease.source.scheduler, group: lease.source.request?.group, order: lease.source.request?.order,
    })), lease};
    visualOwner.current = owner;
    const stop = observeVisibleArtwork(target, owner);
    return () => {
      owner.retire(); stop();
      if (visualOwner.current === owner) visualOwner.current = undefined;
    };
  }, [read.lease]);
  useLayoutEffect(() => {
    const owner = visualOwner.current;
    if (owner && read.status === 'error' && owner.lease === read.lease) owner.fail();
  }, [read.status, read.lease]);
  useLayoutEffect(() => {
    const image = mount.image, lease = mount.lease, imageElement = element.current;
    if (!image || !lease?.live || !imageElement) return;
    const visual = visualOwner.current;
    if (!visual || visual.lease !== lease) return;
    const current = () => owner.live && lease.live && currentMount.current === mount && mount.owner === owner
      && element.current === imageElement && imageElement.isConnected && imageElement.getAttribute('src') === image.src;
    const paint = createArtworkPaintLifetime(imageElement, current);
    // Establish this committed node's real hidden CSS start before a fast decode
    // resolves; admission can then start the native fade without a React commit.
    void imageElement.ownerDocument.defaultView?.getComputedStyle(imageElement).opacity;
    const owner: DecodeOwner = {live: true, decoder: createArtworkDecodeLifetime<ArtworkImage>({
      isCurrent: decode => current() && decode.image === image && decode.element === imageElement,
      receive: (_decode, failed) => {
        setDecoded({mount, owner, failed});
        if (failed) {paint.retire(); visual.fail();}
        else visual.decoded(owner, read.cached === true, current, paint.reveal);
      },
    })};
    mount.owner = owner; visual.begin(image, owner);
    // decode() waits for this actual mounted src, including cache hits and
    // Strict replay. No complete/naturalWidth guess can force decode success.
    owner.decoder.decode(image, imageElement);
    return () => {owner.live = false; paint.retire(); owner.decoder.retire(); if (mount.owner === owner) mount.owner = undefined;};
  }, [mount, read.cached]);
  useEffect(() => {
    const lease = read.lease;
    if (!lease?.live || currentMount.current !== mount || mount.lease !== lease) {
      if (status === 'empty') onStatusChange?.('empty');
      return;
    }
    if (lease.reportedStatus !== status) {lease.reportedStatus = status; lease.callbacks.onStatusChange?.(status);}
    const original = lease.source;
    if (ready) {
      reportedError.current = undefined;
      if (delivered.current?.image !== read.image || delivered.current?.key !== original.key || delivered.current?.scheduler !== original.scheduler) {
        delivered.current = {image: read.image, key: original.key, scheduler: original.scheduler}; lease.callbacks.onReady?.(original);
      }
    }
    if (status === 'error' && (reportedError.current?.key !== original.key || reportedError.current?.scheduler !== original.scheduler)) {
      reportedError.current = {key: original.key, scheduler: original.scheduler}; lease.callbacks.onError?.(original);
    }
  }, [ready, status, read.lease, read.image, mount, onStatusChange]);
  const arrivalStyle = typeof arrivalDurationMs === 'number' && Number.isFinite(arrivalDurationMs) && arrivalDurationMs >= 0 ? {'--art-arrival-duration': `${arrivalDurationMs}ms`} as CSSProperties : undefined;
  // A source lease also owns its observed DOM target. Queued observations of an
  // old target cannot be adopted by its replacement, even with coarse clocks.
  return <span key={read.lease?.id ?? 'empty'} ref={container} className={artClass(styles.artwork, className)} style={artSize({width, height, style: {...arrivalStyle, ...style}})} data-artwork-status={status} data-artwork-cached={read.cached || undefined} role={alt ? 'img' : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true}>
    {fallback != null && <span className={styles.artworkFallback}>{fallback}</span>}
    {read.image && !failed && <img ref={element} key={mount.id} src={read.image.src} alt="" draggable={false} className={styles.image}
      style={{objectFit: fit, objectPosition: position, transform: zoom === 1 ? undefined : `scale(${zoom})`, filter: saturation === 1 && brightness === 1 ? undefined : `saturate(${saturation}) brightness(${brightness})`}}
      onError={event => {if (mount.image) mount.owner?.decoder.fail(mount.image, event.currentTarget);}} />}
  </span>;
}
