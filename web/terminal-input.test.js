import { describe, expect, test } from 'bun:test';

import { SHIFT_ENTER_SEQUENCE, shiftEnterAction } from './terminal-input.js';

describe('terminal modified Enter', () => {
  const shiftEnter = {
    type: 'keydown',
    key: 'Enter',
    shiftKey: true,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    isComposing: false,
  };

  test('encodes plain Shift+Enter with Kitty CSI-u', () => {
    expect(SHIFT_ENTER_SEQUENCE).toBe('\u001b[13;2u');
    expect(shiftEnterAction(shiftEnter)).toBe('send');
  });

  test('suppresses the matching keypress so it cannot append carriage return', () => {
    expect(shiftEnterAction({ ...shiftEnter, type: 'keypress' })).toBe(
      'suppress',
    );
    expect(shiftEnterAction({ ...shiftEnter, type: 'keyup' })).toBeNull();
  });

  test('does not intercept plain Enter, other modifiers, or composition', () => {
    expect(shiftEnterAction({ ...shiftEnter, shiftKey: false })).toBeNull();
    expect(shiftEnterAction({ ...shiftEnter, ctrlKey: true })).toBeNull();
    expect(shiftEnterAction({ ...shiftEnter, altKey: true })).toBeNull();
    expect(shiftEnterAction({ ...shiftEnter, metaKey: true })).toBeNull();
    expect(shiftEnterAction({ ...shiftEnter, isComposing: true })).toBeNull();
    expect(shiftEnterAction({ ...shiftEnter, key: 'Tab' })).toBeNull();
  });
});
