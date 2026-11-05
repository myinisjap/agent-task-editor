// Pooled particle system for the office scene. Pure (no DOM): struct-of-arrays
// typed storage allocated once, a free-slot stack for O(1) spawn, and
// round-robin recycling when the cap is hit, so the live count never exceeds
// the cap and stepping/spawning allocates nothing. Colors are indices into
// PARTICLE_COLORS (no per-particle strings). The renderer loops 0..cap-1 over
// the exposed arrays and skips slots whose `alive` is 0.

export const SPARK = 0
export const PAPER = 1
export const STEAM = 2
export const LED = 3
export const CONFETTI = 4
export const DUST = 5
export type ParticleKind = 0 | 1 | 2 | 3 | 4 | 5

export const PARTICLE_CAP = 200

// Palette indices by group: sparks 0-2, paper 3-4, steam 5-7, LEDs 8-10,
// confetti 11-15, dust 16-18.
export const PARTICLE_COLORS: readonly string[] = [
  '#fde047', '#fb923c', '#ffffff',
  '#f8fafc', '#cbd5e1',
  '#e2e8f0', '#94a3b8', '#cbd5e1',
  '#22d3ee', '#f472b6', '#4ade80',
  '#f43f5e', '#facc15', '#38bdf8', '#a3e635', '#c084fc',
  '#d9c3a0', '#c8a77e', '#a88a64',
]

export class ParticlePool {
  readonly cap: number
  readonly x: Float32Array
  readonly y: Float32Array
  readonly vx: Float32Array
  readonly vy: Float32Array
  readonly ay: Float32Array
  readonly life: Float32Array
  readonly maxLife: Float32Array
  readonly kind: Uint8Array
  readonly color: Uint8Array
  readonly alive: Uint8Array
  private free: Uint16Array
  private freeTop = 0
  private steal = 0
  private live = 0

  constructor(cap = PARTICLE_CAP) {
    this.cap = cap
    this.x = new Float32Array(cap)
    this.y = new Float32Array(cap)
    this.vx = new Float32Array(cap)
    this.vy = new Float32Array(cap)
    this.ay = new Float32Array(cap)
    this.life = new Float32Array(cap)
    this.maxLife = new Float32Array(cap)
    this.kind = new Uint8Array(cap)
    this.color = new Uint8Array(cap)
    this.alive = new Uint8Array(cap)
    this.free = new Uint16Array(cap)
    this.clear()
  }

  /** Spawn one particle; returns its slot. At the cap the oldest-stolen slot (round-robin) is recycled. */
  spawn(kind: ParticleKind, x: number, y: number, vx: number, vy: number, ay: number, life: number, color: number): number {
    let i: number
    if (this.freeTop > 0) {
      i = this.free[--this.freeTop]
      this.live++
    } else {
      i = this.steal
      this.steal = (this.steal + 1) % this.cap
    }
    this.x[i] = x
    this.y[i] = y
    this.vx[i] = vx
    this.vy[i] = vy
    this.ay[i] = ay
    this.life[i] = life
    this.maxLife[i] = life
    this.kind[i] = kind
    this.color[i] = color
    this.alive[i] = 1
    return i
  }

  step(dt: number): void {
    for (let i = 0; i < this.cap; i++) {
      if (!this.alive[i]) continue
      const life = this.life[i] - dt
      if (life <= 0) {
        this.alive[i] = 0
        this.free[this.freeTop++] = i
        this.live--
        continue
      }
      this.life[i] = life
      this.vy[i] += this.ay[i] * dt
      this.x[i] += this.vx[i] * dt
      this.y[i] += this.vy[i] * dt
      // Paper bits flutter side to side as they fall.
      if (this.kind[i] === PAPER) this.x[i] += Math.sin(life * 7 + i) * 24 * dt
    }
  }

  clear(): void {
    this.alive.fill(0)
    for (let k = 0; k < this.cap; k++) this.free[k] = this.cap - 1 - k // pops 0 first
    this.freeTop = this.cap
    this.steal = 0
    this.live = 0
  }

  count(): number {
    return this.live
  }
}
