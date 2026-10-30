/**
 * Generation discipline (spec §2 presentation, §6, invariant 3). Every URL belongs to one
 * `presentation.generation`; a change that alters bytes (track needing transcode, quality,
 * version, burn-in) makes a new one. The gate only moves forward, and anything tagged with an
 * older generation (a late player event, a report built before the switch) is refused.
 */
export class GenerationGate {
  private current: number | undefined;

  /** The generation in force, if any. */
  get value(): number | undefined {
    return this.current;
  }

  /**
   * Adopt a generation from the server. True when it is newer (the player must load its URL);
   * false for the same or an older one (a replayed or reordered response), which is ignored.
   */
  advance(generation: number): boolean {
    if (!Number.isSafeInteger(generation) || generation < 0) return false;
    if (this.current !== undefined && generation <= this.current) return false;
    this.current = generation;
    return true;
  }

  /** Whether something tagged with `generation` still speaks for the current presentation. */
  accepts(generation: number): boolean {
    return this.current !== undefined && generation === this.current;
  }

  reset(): void {
    this.current = undefined;
  }
}
