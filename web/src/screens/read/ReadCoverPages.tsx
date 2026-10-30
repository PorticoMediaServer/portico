import {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ReadingSession} from '@portico/client-core';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
import type {ScreenContext} from '../detail/context';
import {createReadBindings, type ReadBindingContext} from './bindings';
import styles from './cover-pages.module.css';

const {t} = defaultI18n;
type Opening = Readonly<{controller: AbortController; isCurrent: () => boolean}>;
/** CBZ pages are actual admitted archive images. No PDF page count or EPUB page is inferred. */
export function ReadCoverPages({reading, context, itemId, sourceId, upload, busy}: {reading: ReadBindingContext; context: ScreenContext; itemId: string; sourceId: string; upload: (file: Blob) => Promise<boolean>; busy: boolean}) {
  const [opening, setOpening] = useState<Opening>(), [session, setSession] = useState<ReadingSession>(), [failed, setFailed] = useState(false);
  const activeOpening = useRef<Opening | undefined>(undefined), inputs = useRef({reading, context, itemId, sourceId});
  inputs.current = {reading, context, itemId, sourceId};
  const trigger = useRef<HTMLButtonElement>(null);
  const retire = useCallback(() => {const held = activeOpening.current; activeOpening.current = undefined; held?.controller.abort();}, []);
  useLayoutEffect(() => retire, [retire, reading, context, itemId, sourceId]);
  const begin = () => {
    retire();
    if (!context.isCurrent()) return;
    const bound = inputs.current, controller = new AbortController();
    const held: Opening = {controller, isCurrent: () => activeOpening.current === held && !controller.signal.aborted && bound.context.isCurrent()
      && inputs.current.reading === bound.reading && inputs.current.context === bound.context && inputs.current.itemId === bound.itemId && inputs.current.sourceId === bound.sourceId};
    activeOpening.current = held; setSession(undefined); setFailed(false); setOpening(held);
  };
  const close = (held: Opening) => {if (activeOpening.current !== held) return; retire(); setOpening(undefined);};
  useEffect(() => {
    if (!opening?.isCurrent()) return;
    const bindings = createReadBindings(reading); let active = true;
    setSession(undefined); setFailed(false);
    const stop = () => {active = false; void bindings.dispose().catch(() => {});};
    opening.controller.signal.addEventListener('abort', stop, {once: true});
    const off = bindings.authority.subscribe(() => {if (active && opening.isCurrent()) setFailed(true);});
    void bindings.open({itemId, sourceId}).then(value => {
      if (!active || !opening.isCurrent() || !bindings.authority.isCurrent()) return;
      if (value.manifest.format !== 'cbz' || !value.manifest.pageNames?.length) throw new Error('invalid_cover_source');
      setSession(value);
    }).catch(error => {if (active && opening.isCurrent()) {setFailed(true); context.effects.reportError(error);}});
    return () => {opening.controller.signal.removeEventListener('abort', stop); off(); stop();};
  }, [opening, reading, context, itemId, sourceId]);
  return <><Button ref={trigger} size="small" disabled={busy} onClick={begin}>{t('next.reader.chooseCoverPage')}</Button>
    {opening?.isCurrent() && <Dialog open title={t('next.reader.chooseCoverPage')} returnFocusRef={trigger} onOpenChange={value => {if (!value) close(opening);}}>
      {failed ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: begin}}/>
        : session ? <Pages key={session.manifest.sessionId} session={session} context={context} opening={opening} upload={upload} busy={busy} onChosen={() => {if (opening.isCurrent()) close(opening);}}/> : <Skeleton height={280}/>}
    </Dialog>}
  </>;
}
function Pages({session, context, opening, upload, busy, onChosen}: {session: ReadingSession; context: ScreenContext; opening: Opening; upload: (file: Blob) => Promise<boolean>; busy: boolean; onChosen: () => void}) {
  const [start, setStart] = useState(0), [pages, setPages] = useState<readonly Readonly<{index: number; url: string}>[]>([]), [failed, setFailed] = useState(false), [choosing, setChoosing] = useState(false), [retry, setRetry] = useState(0);
  const choice = useRef<AbortController | undefined>(undefined), requests = useRef(new Set<AbortController>()), pending = useRef(false);
  const count = session.manifest.pageNames!.length, end = Math.min(start + 6, count);
  useEffect(() => {const owned = requests.current; return () => {for (const request of owned) request.abort(); owned.clear();};}, []);
  useEffect(() => {
    const operation = new AbortController(), signal = AbortSignal.any([operation.signal, opening.controller.signal]), urls: string[] = []; setPages([]); setFailed(false);
    void (async () => {
      for (let index = start; index < end; index++) {
        if (!opening.isCurrent()) return;
        const image = await session.page(index, {width: 180, signal});
        if (signal.aborted || !opening.isCurrent()) return;
        const url = URL.createObjectURL(new Blob([image.bytes.slice() as Uint8Array<ArrayBuffer>], {type: image.mime})); urls.push(url);
        setPages(previous => [...previous, {index, url}]);
      }
    })().catch(error => {if (!signal.aborted && opening.isCurrent()) {setFailed(true); context.effects.reportError(error);}});
    return () => {operation.abort(); for (const url of urls) URL.revokeObjectURL(url);};
  }, [session, context, opening, start, end, retry]);
  const choose = async (index: number) => {
    if (busy || pending.current || !opening.isCurrent() || session.signal.aborted) return;
    const operation = new AbortController(), signal = AbortSignal.any([operation.signal, opening.controller.signal]); choice.current = operation; requests.current.add(operation); pending.current = true; setChoosing(true); setFailed(false);
    try {
      const image = await session.page(index, {width: 1280, signal});
      if (signal.aborted || !opening.isCurrent()) return;
      const blob = new Blob([image.bytes.slice() as Uint8Array<ArrayBuffer>], {type: image.mime});
      if (blob.size > 10 * 1024 * 1024) throw new Error(t('web.metadata.uploadImageLimit'));
      if (signal.aborted || !opening.isCurrent()) return;
      const saved = await upload(blob);
      if (!signal.aborted && opening.isCurrent()) {if (saved) onChosen(); else setFailed(true);}
    } catch (error) {if (!signal.aborted && opening.isCurrent()) {setFailed(true); context.effects.reportError(error);}}
    finally {requests.current.delete(operation); if (choice.current === operation) {pending.current = false; if (!signal.aborted && opening.isCurrent()) setChoosing(false);}}
  };
  return <><div className={styles.pages}>{pages.map(page => <button key={page.index} type="button" disabled={busy || choosing} onClick={() => {void choose(page.index);}}>
    <img src={page.url} alt=""/><span>{t('next.reader.pageLabel', {page: defaultI18n.number(page.index + 1)})}</span></button>)}</div>
    {failed && <Button disabled={busy || choosing} onClick={() => setRetry(value => value + 1)}>{t('next.states.tryAgain')}</Button>}
    <div className={styles.navigation}><Button size="small" disabled={start === 0 || busy || choosing} onClick={() => setStart(value => Math.max(0, value - 6))}>{t('next.reader.previous')}</Button>
      <Button size="small" disabled={end >= count || busy || choosing} onClick={() => setStart(end)}>{t('next.reader.next')}</Button></div></>;
}
