import { describe, expect, test } from 'bun:test';

import { playCompletionPing } from './completion-audio.js';

describe('completion ping', () => {
  test('schedules and releases the two-tone chime', () => {
    const frequency = [];
    const volume = [];
    let ended;
    let oscillatorDisconnected = false;
    let gainDisconnected = false;
    const gain = {
      gain: {
        setValueAtTime: (...args) => volume.push(['set', ...args]),
        exponentialRampToValueAtTime: (...args) =>
          volume.push(['ramp', ...args]),
      },
      connect: (destination) => expect(destination).toBe('speakers'),
      disconnect: () => {
        gainDisconnected = true;
      },
    };
    const oscillator = {
      type: '',
      frequency: {
        setValueAtTime: (...args) => frequency.push(['set', ...args]),
        exponentialRampToValueAtTime: (...args) =>
          frequency.push(['ramp', ...args]),
      },
      connect: (target) => expect(target).toBe(gain),
      addEventListener: (type, listener, options) => {
        expect(type).toBe('ended');
        expect(options).toEqual({ once: true });
        ended = listener;
      },
      start: (at) => expect(at).toBe(10),
      stop: (at) => expect(at).toBeCloseTo(10.21),
      disconnect: () => {
        oscillatorDisconnected = true;
      },
    };
    const context = {
      state: 'running',
      currentTime: 10,
      destination: 'speakers',
      createOscillator: () => oscillator,
      createGain: () => gain,
    };

    expect(playCompletionPing(context)).toBe(true);
    expect(oscillator.type).toBe('sine');
    expect(frequency).toEqual([
      ['set', 880, 10],
      ['ramp', 1_320, 10.12],
    ]);
    expect(volume).toEqual([
      ['set', 0.0001, 10],
      ['ramp', 0.12, 10.015],
      ['ramp', 0.0001, 10.2],
    ]);
    ended();
    expect(oscillatorDisconnected).toBe(true);
    expect(gainDisconnected).toBe(true);
  });

  test('does not schedule audio without a running context', () => {
    expect(playCompletionPing(null)).toBe(false);
    expect(playCompletionPing({ state: 'suspended' })).toBe(false);
  });
});
