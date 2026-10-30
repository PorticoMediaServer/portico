import {useLayoutEffect, useRef, useState} from 'react';
import {randomId} from '@core/random-id.ts';
import {isUserMutationKey, parseUserMutationReceipt, UserMutationError} from '@core/user-mutations.ts';
import {createReadingMarkAllCommand, parseReadingMarkAllProjection, parseReadingMarkAllReceipt, readingMarkAllPath,
  type ReadingMarkAllCommand, type ReadingMarkAllProjection} from '@core/reading-mark-all.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

type Opening = {context: ScreenContext; libraryId: string; routeKey: string; life: ReturnType<typeof screenLifetime>; live: boolean; busy: boolean; review?: boolean;
  proof?: ReadingMarkAllProjection; command?: ReadingMarkAllCommand; onCommitted: () => void};
type View = Readonly<{opening?: Opening; proof?: ReadingMarkAllProjection; busy?: boolean}>;
const reviewCodes = new Set(['personal_state_conflict', 'personal_needs_resolution', 'operation_expired', 'idempotency_key_reused',
  'stale_continuation', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized', 'receipt_expired', 'idempotency_conflict', 'mutation_needs_review', 'revision_mismatch']);
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);

/** A whole-library explicit Reading intent, captured to this original opening.
 * A projection is only a CAS witness; POST always re-admits its original actor.
 * Unknown transport outcomes retain the same proof/body/operation for explicit
 * retry. Nothing rewrites an uncertain operation against a fresher revision. */
export function useReadingMarkAll(context: ScreenContext, libraryId: string, routeKey: string, onCommitted: () => void) {
  const inputs = useRef({context, libraryId, routeKey}); inputs.current = {context, libraryId, routeKey};
  const opening = useRef<Opening | undefined>(undefined), [view, setView] = useState<View>({}), shown = useRef<View>(view);
  const publish = (next: View) => {shown.current = next; setView(next);};
  const current = (held: Opening) => held.live && opening.current === held && held.life.current()
    && inputs.current.context === held.context && inputs.current.libraryId === held.libraryId && inputs.current.routeKey === held.routeKey;
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({onCommitted});
  useLayoutEffect(() => {setupPorts.current = {onCommitted};});
  useLayoutEffect(() => {
    const {onCommitted} = setupPorts.current;
    const viewer = context.identity?.viewer, readPath = readingMarkAllPath('GET', libraryId), writePath = readingMarkAllPath('POST', libraryId);
    if (!viewer || !['local', 'hosted'].includes(viewer.authority) || !viewer.accountId || !viewer.profileId
      || !['owner', 'admin', 'member'].includes(viewer.role) || !readPath || !writePath) {publish({}); return;}
    const held: Opening = {context, libraryId, routeKey, life: screenLifetime(context), live: true, busy: true, onCommitted};
    opening.current = held; publish({opening: held, busy: true});
    void held.life.api.request(readPath, 'GET').then(raw => {
      const proof = parseReadingMarkAllProjection(raw, context.scope.serverId, libraryId);
      if (!current(held)) return;
      held.proof = proof; held.busy = false; publish({opening: held, proof});
    }).catch(error => {if (current(held)) {held.busy = false; publish({opening: held}); context.effects.reportError(error);}});
    return () => {held.live = false; held.life.dispose(); if (opening.current === held) opening.current = undefined;};
    // onCommitted is captured with the opening and never adopted by a pending
    // request. Parent refresh is allowed only while this opening remains live.
  }, [context, libraryId, routeKey]);
  const held = view.opening, proof = view.proof;
  if (!held || !proof || !current(held) || held.busy || held.review || proof.count === 0) return undefined;
  const rendered = view, command = held.command;
  return () => {
    if (!current(held) || held.busy || held.review || shown.current !== rendered || held.proof !== proof || held.command !== command) return;
    const path = readingMarkAllPath('POST', held.libraryId);
    if (!path) return;
    const operation = command ?? createReadingMarkAllCommand(proof, randomId());
    held.command = operation; held.busy = true; publish({opening: held, proof, busy: true});
    void held.life.api.request(path, 'POST', operation).then(async raw => {
      if (!current(held) || held.command !== operation) return;
      if (object(raw) && raw.mutationReceipt !== undefined) {
        const marker = raw.mutationReceipt;
        if (!object(marker) || typeof marker.key !== 'string' || !isUserMutationKey(marker.key)) throw new Error('invalid_reading_mark_all');
        const receipt = parseUserMutationReceipt(marker, {key: marker.key, operation: 'post_libraries_libraryId_reading_mark_all'});
        if (receipt.targetId !== held.libraryId) throw new Error('invalid_reading_mark_all');
        if (receipt.status !== 'applied') throw new UserMutationError('mutation_needs_review', receipt.key, receipt);
        const readPath = readingMarkAllPath('GET', held.libraryId);
        if (!readPath) throw new Error('invalid_reading_mark_all');
        const rawCurrent = await held.life.api.request(readPath, 'GET');
        if (!current(held) || held.command !== operation) return;
        const fresh = parseReadingMarkAllProjection(rawCurrent, held.context.scope.serverId, held.libraryId);
        if (fresh.viewerFence !== proof.viewerFence || fresh.revision.catalog < proof.revision.catalog || receipt.revision === undefined || fresh.revision.viewer < receipt.revision) throw new Error('invalid_reading_mark_all');
        // Current counts belong to this read. No historical changed/count result is reconstructed.
      } else parseReadingMarkAllReceipt(raw, proof, operation);
      if (!current(held) || held.command !== operation) return;
      await held.context.api.acknowledgeUserMutation?.(operation.operationId);
      if (!current(held) || held.command !== operation) return;
      held.command = undefined;
      // The receipt describes the old operation. Remount actual collections,
      // summary and action proof rather than presenting its count as new state.
      held.onCommitted();
    }).catch(error => {
      if (!current(held) || held.command !== operation) return;
      held.context.effects.reportError(error);
      if (object(error) && typeof error.code === 'string' && reviewCodes.has(error.code)) held.review = true;
      // Keep the original proof and command through conflicts and ambiguous failures.
      // A fresh projection is not permission to author a replacement operation.
      held.busy = false; publish({opening: held, proof});
    }).finally(() => {held.busy = false;});
  };
}
