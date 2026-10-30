import {springs, type Spring} from '@design';

export type MotionSample = Readonly<{position: number; velocity: number}>;
/** Analytic spring in physical units. Retargeting preserves both position and velocity. */
export class PlayerMorph {
  private origin: MotionSample; private target: number; private started = 0; private spring: Spring;
  constructor(position: number, spring: Spring = springs.player) { this.origin = {position, velocity: 0}; this.target = position; this.spring = spring; }
  sample(nowMs: number): MotionSample {
    const t = Math.max(0, (nowMs - this.started) / 1000), {damping, stiffness, mass} = this.spring;
    if (t === 0) return this.origin;
    const decay = -damping / (2 * mass), frequency = Math.sqrt(Math.max(0, stiffness / mass - decay * decay));
    const displacement = this.origin.position - this.target;
    if (frequency === 0) {
      const c = this.origin.velocity - decay * displacement, e = Math.exp(decay * t);
      return {position: this.target + e * (displacement + c * t), velocity: e * (decay * (displacement + c * t) + c)};
    }
    const b = (this.origin.velocity - decay * displacement) / frequency, cos = Math.cos(frequency * t), sin = Math.sin(frequency * t), e = Math.exp(decay * t);
    const value = displacement * cos + b * sin;
    return {position: this.target + e * value, velocity: e * (decay * value + frequency * (-displacement * sin + b * cos))};
  }
  retarget(target: number, nowMs: number, velocity?: number): MotionSample {
    const current = this.sample(nowMs); this.origin = {position: current.position, velocity: velocity ?? current.velocity}; this.target = target; this.started = nowMs; return this.origin;
  }
  track(position: number, velocity: number, nowMs: number) { this.origin = {position, velocity}; this.target = position; this.started = nowMs; }
  settled(nowMs: number, tolerance = .002): boolean { const state = this.sample(nowMs); return Math.abs(state.position - this.target) < tolerance && Math.abs(state.velocity) < tolerance; }
}

export const GestureRelease = {
  player(position: number, velocityPixels: number, travelPixels: number): Readonly<{target: 0 | 1; velocity: number}> {
    const travel = Math.max(1, Math.abs(travelPixels));
    return {target: velocityPixels > 600 ? 1 : velocityPixels < -600 ? 0 : position > .35 ? 1 : 0, velocity: velocityPixels / travel};
  },
  dismiss(positionPixels: number, velocityPixels: number, width: number): Readonly<{dismiss: boolean; target: number; velocity: number}> {
    const committed = Math.abs(positionPixels) > Math.max(1, width) / 3 || Math.abs(velocityPixels) > 800;
    const direction = Math.abs(velocityPixels) > 800 ? Math.sign(velocityPixels) : Math.sign(positionPixels);
    return {dismiss: committed, target: committed ? direction * Math.max(1, width) : 0, velocity: velocityPixels};
  },
} as const;
