import { describe, it, expect } from 'vitest'
import { ParticlePool, PARTICLE_CAP, SPARK, PAPER, PARTICLE_COLORS } from './officeParticles'

describe('ParticlePool', () => {
  it('caps the live count when over-spawned', () => {
    const p = new ParticlePool(PARTICLE_CAP)
    for (let i = 0; i < 250; i++) {
      p.spawn(SPARK, 0, 0, 0, 0, 0, 1, 0)
      expect(p.count()).toBeLessThanOrEqual(PARTICLE_CAP)
    }
    expect(p.count()).toBe(PARTICLE_CAP)
  })

  it('frees dead particles and reuses their slots', () => {
    const p = new ParticlePool(50)
    const first = new Set<number>()
    for (let i = 0; i < 10; i++) first.add(p.spawn(SPARK, 0, 0, 0, 0, 0, 0.5, 0))
    p.step(0.6)
    expect(p.count()).toBe(0)
    for (let i = 0; i < 10; i++) {
      const slot = p.spawn(SPARK, 0, 0, 0, 0, 0, 0.5, 0)
      expect(slot).toBeGreaterThanOrEqual(0)
      expect(slot).toBeLessThan(50)
      expect(first.has(slot)).toBe(true)
    }
    expect(p.count()).toBe(10)
  })

  it('overwrites round-robin when full', () => {
    const p = new ParticlePool(8)
    for (let i = 0; i < 8; i++) p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)
    expect(p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)).toBe(0)
    expect(p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)).toBe(1)
    expect(p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)).toBe(2)
    expect(p.count()).toBe(8)
  })

  it('clear() kills everything and restores the pool', () => {
    const p = new ParticlePool(8)
    for (let i = 0; i < 12; i++) p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)
    p.clear()
    expect(p.count()).toBe(0)
    expect(p.spawn(SPARK, 0, 0, 0, 0, 0, 5, 0)).toBe(0)
    expect(p.count()).toBe(1)
  })

  it('integrates velocity and gravity', () => {
    const p = new ParticlePool(4)
    const i = p.spawn(SPARK, 10, 20, 100, -50, 200, 5, 0)
    p.step(0.1)
    expect(p.x[i]).toBeCloseTo(20)
    expect(p.vy[i]).toBeCloseTo(-30) // -50 + 200*0.1
    expect(p.y[i]).toBeCloseTo(20 - 3) // y += vy(after gravity) * dt
  })

  it('flutters paper sideways with no velocity', () => {
    const p = new ParticlePool(4)
    const i = p.spawn(PAPER, 10, 0, 0, 10, 0, 5, 3)
    for (let k = 0; k < 10; k++) p.step(0.05)
    expect(p.x[i]).not.toBe(10)
  })

  it('never reallocates its typed arrays', () => {
    const p = new ParticlePool(32)
    const refs = [p.x, p.y, p.vx, p.vy, p.life, p.alive, p.kind, p.color]
    for (let k = 0; k < 1000; k++) {
      p.spawn(SPARK, k, k, 1, 1, 1, 0.05, k % PARTICLE_COLORS.length)
      p.step(0.01)
    }
    expect([p.x, p.y, p.vx, p.vy, p.life, p.alive, p.kind, p.color]).toEqual(refs)
    refs.forEach((r, n) => expect([p.x, p.y, p.vx, p.vy, p.life, p.alive, p.kind, p.color][n]).toBe(r))
  })
})
