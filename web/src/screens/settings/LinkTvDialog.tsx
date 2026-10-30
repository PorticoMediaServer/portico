import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../../kit/overlays';
import type {PlayOnLinkPreview} from '../../platform/playback/play-on';
import {watchLinkTv} from '../watch/receivers';
import {LinkTvForm} from './LinkTvForm';

const t = defaultI18n.t;

type LinkApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
/** One committed open of the dialog: its own api context, abort signal and one-flight field. */
type Opening = {controller: AbortController; api: LinkApi; port: ReturnType<typeof watchLinkTv>; busy: boolean};
type Review = {opening: Opening; preview: PlayOnLinkPreview};

/** Settings › This device › Link a TV. The same real quick-connect services the Play on
 * sheet uses: the phone reads the code the TV shows, sees the device it reports, and
 * records its own approval. The TV collects its session in its own request. */
export function LinkTvDialog({open, onOpenChange, api, returnFocusRef, initialCode}: {
  open: boolean; onOpenChange(open: boolean): void;
  api: LinkApi;
  returnFocusRef: React.RefObject<HTMLElement | null>;
  /** Display-only code captured once for this mount's first committed open (e.g. the local
   * entry's /device fragment). It seeds the field and nothing else: no review, approval or
   * request, and later prop changes never reach an existing opening. Settings omits it. */
  initialCode?: string;
}) {
  const [opening, setOpening] = useState<Opening | null>(null);
  const [code, setCode] = useState(''), [review, setReview] = useState<Review | undefined>(undefined);
  const seed = useRef({value: initialCode?.slice(0, 10) ?? '', api: undefined as LinkApi | undefined, spent: false});
  const draft = useRef<Readonly<{api: LinkApi; code: string}> | undefined>(undefined);
  const [phase, setPhase] = useState<'code' | 'busy' | 'review' | 'done' | 'failed'>('code');
  const liveOpening = useRef<Opening | null>(null);
  const clear = () => { setCode(''); setReview(undefined); setPhase('code'); };
  const live = (held: Opening | null): held is Opening => !!held && liveOpening.current === held && !held.controller.signal.aborted;

  // Admission and retirement are layout-committed: an external close, api change or unmount
  // retires the old opening synchronously in the same commit, before paint and before any
  // passive cleanup. The rendered callbacks below capture the opening of their own render, so
  // a stale callback can only ever act on the opening it was rendered for.
  useLayoutEffect(() => {
    if (!open) {
      if (seed.current.api) seed.current.spent = true;
      draft.current = undefined;
      setOpening(null);
      clear();
      return;
    }
    if (seed.current.api && seed.current.api !== api) seed.current.spent = true;
    const initial = draft.current?.api === api ? draft.current.code : seed.current.spent ? '' : seed.current.value;
    seed.current.api ??= api;
    draft.current = {api, code: initial};
    const next: Opening = {controller: new AbortController(), api, port: watchLinkTv({request: (path, method, body, signal) => api.request(path, method, body, signal)}), busy: false};
    liveOpening.current = next;
    setOpening(next);
    setReview(undefined); setPhase('code');
    setCode(initial);
    // Replay gets a fresh signal and port, retaining only the display draft. An
    // aborted owner is never installed again; cleanup cannot retire a successor.
    return () => {if (liveOpening.current === next) liveOpening.current = null;next.controller.abort();};
  }, [open, api]);

  const look = async (held: Opening | null) => {
    if (!live(held) || held.busy) return;
    const value = code.trim().toUpperCase();
    if (value.length < 4) { setPhase('failed'); return; }
    held.busy = true;
    setPhase('busy');
    try {
      const preview = await held.port.preview(value, {signal: held.controller.signal});
      if (!live(held)) return;
      setReview({opening: held, preview});
      setPhase('review');
    } catch {
      if (live(held)) { setReview(undefined); setPhase('failed'); }
    } finally { held.busy = false; }
  };
  const decide = async (held: Opening | null, decision: 'approve' | 'deny') => {
    const captured = review;
    if (!live(held) || held.busy || !captured || captured.opening !== held) return;
    held.busy = true;
    setPhase('busy');
    try {
      await held.port.decide(captured.preview.requestId, captured.preview.userCode, decision, {signal: held.controller.signal});
      if (!live(held)) return;
      if (decision === 'approve') setPhase('done');
      else { liveOpening.current = null; seed.current.spent = true; draft.current = undefined; held.controller.abort(); setOpening(null); clear(); onOpenChange(false); }
    } catch {
      if (live(held)) setPhase('failed');
    } finally { held.busy = false; }
  };
  const close = (held: Opening | null) => {
    if (!live(held)) return;
    liveOpening.current = null;
    seed.current.spent = true;draft.current = undefined;
    held.controller.abort();
    setOpening(null);
    clear();
    onOpenChange(false);
  };
  const changeCode = (held: Opening | null, value: string) => {
    if (!live(held)) return;
    draft.current = {api: held.api, code: value.toUpperCase()};
    setCode(value.toUpperCase());
    if (phase === 'failed') setPhase('code');
  };
  const admitted = open && opening?.api === api && liveOpening.current === opening && !opening.controller.signal.aborted;
  const preview = admitted && review?.opening === opening ? review.preview : undefined;
  return <Dialog open={open} onOpenChange={value => { if (value) {if (live(opening)) onOpenChange(true);} else close(opening); }} title={t('next.cast.linkTv')} returnFocusRef={returnFocusRef}>
    {admitted && <LinkTvForm code={code} phase={phase} preview={preview}
      onCodeChange={value => { changeCode(opening, value); }}
      onReview={() => { void look(opening); }}
      onDecide={decision => { void decide(opening, decision); }} />}
  </Dialog>;
}
