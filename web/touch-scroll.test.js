import { describe, expect, test } from 'bun:test';
import {
  createScrollStepper,
  createVelocityTracker,
  MIN_MOMENTUM_VELOCITY,
  momentumStep,
  wheelEventPixels,
} from './touch-scroll.js';

describe('scroll stepper', () => {
  test.each([
    ['small moves', 5],
    ['medium moves', 20],
    ['large moves', 150],
  ])('turns the same distance into the same steps for %s', (_name, move) => {
    const stepper = createScrollStepper();
    let steps = 0;
    for (let travelled = 0; travelled < 600; travelled += move) {
      steps += stepper.take(move, 57);
    }
    expect(steps).toBe(10);
  });

  test('a reversal cancels the carried remainder', () => {
    const stepper = createScrollStepper();
    expect(stepper.take(50, 57)).toBe(0);
    expect(stepper.take(-50, 57)).toBe(0);
    expect(stepper.take(-60, 57)).toBe(-1);
  });

  test('reset drops a partial step', () => {
    const stepper = createScrollStepper();
    stepper.take(50, 57);
    stepper.reset();
    expect(stepper.take(10, 57)).toBe(0);
  });
});

describe('wheel event pixels', () => {
  test('converts line and page deltas through the row height', () => {
    expect(wheelEventPixels({ deltaMode: 0, deltaY: 42 }, 19, 40)).toBe(42);
    expect(wheelEventPixels({ deltaMode: 1, deltaY: 3 }, 19, 40)).toBe(57);
    expect(wheelEventPixels({ deltaMode: 2, deltaY: -1 }, 19, 40)).toBe(-760);
  });
});

describe('release velocity', () => {
  test('measures a steady upward drag as positive scroll speed', () => {
    const tracker = createVelocityTracker();
    tracker.reset(0, 500);
    for (let frame = 1; frame <= 10; frame += 1) {
      tracker.add(frame * 16, 500 - frame * 32);
    }
    expect(tracker.releaseVelocity(165)).toBeCloseTo(2, 5);
  });

  test('uses only the end of the drag', () => {
    const tracker = createVelocityTracker();
    tracker.reset(0, 0);
    tracker.add(100, -1_000);
    tracker.add(200, -1_010);
    tracker.add(250, -1_020);
    expect(tracker.releaseVelocity(255)).toBeCloseTo(0.2, 5);
  });

  test('a finger that rested before lifting does not fling', () => {
    const tracker = createVelocityTracker();
    tracker.reset(0, 500);
    tracker.add(16, 400);
    tracker.add(32, 300);
    expect(tracker.releaseVelocity(120)).toBe(0);
  });

  test('bursts with identical timestamps stay bounded', () => {
    const tracker = createVelocityTracker();
    tracker.reset(10, 800);
    tracker.add(10, 200);
    const velocity = tracker.releaseVelocity(10);
    expect(Number.isFinite(velocity)).toBe(true);
    expect(velocity).toBeLessThanOrEqual(8);
  });
});

describe('momentum', () => {
  function glide(velocity, frameMs) {
    let distance = 0;
    let elapsed = 0;
    while (Math.abs(velocity) >= MIN_MOMENTUM_VELOCITY) {
      const step = momentumStep(velocity, frameMs);
      distance += step.distance;
      velocity = step.velocity;
      elapsed += frameMs;
    }
    return { distance, elapsed };
  }

  test('travels the same distance at 60 Hz and 120 Hz', () => {
    const at60 = glide(2, 16);
    const at120 = glide(2, 8);
    expect(Math.abs(at60.distance - at120.distance)).toBeLessThan(8);
  });

  test('carries a 2 px/ms fling about a thousand pixels and stops', () => {
    const { distance, elapsed } = glide(2, 16);
    expect(distance).toBeGreaterThan(950);
    expect(distance).toBeLessThan(1_000);
    expect(elapsed).toBeLessThan(2_500);
  });

  test('glides in the direction of the fling', () => {
    expect(glide(-2, 16).distance).toBeLessThan(-950);
  });
});
