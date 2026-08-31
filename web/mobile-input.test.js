import { describe, expect, test } from 'bun:test';

import {
  applyTerminalInput,
  configureNaturalTextInput,
  correctionForTerminalInput,
  mobileSwitcherMouseSequence,
  terminalTypingActive,
  usesNativeMobileTextInput,
} from './mobile-input.js';

function fakeInput() {
  const attributes = new Map();
  return {
    attributes,
    setAttribute(name, value) {
      attributes.set(name, value);
    },
  };
}

describe('mobile terminal input', () => {
  test('enables native writing assistance', () => {
    const input = fakeInput();
    configureNaturalTextInput(input);

    expect(Object.fromEntries(input.attributes)).toEqual({
      autocomplete: 'on',
      autocapitalize: 'sentences',
      autocorrect: 'on',
      spellcheck: 'true',
      inputmode: 'text',
    });
  });

  test('reports desktop and explicitly enabled mobile typing state', () => {
    expect(terminalTypingActive(true, true, true, false)).toBe(true);
    expect(terminalTypingActive(true, true, false, false)).toBe(false);
    expect(terminalTypingActive(true, true, false, true)).toBe(true);
    expect(terminalTypingActive(false, true, false, true)).toBe(false);
  });

  test('targets Herdr’s fixed mobile switch hit area with SGR mouse input', () => {
    expect(mobileSwitcherMouseSequence(44)).toBe(
      '\u001b[<0;36;2M\u001b[<0;36;2m',
    );
    expect(mobileSwitcherMouseSequence(8)).toBe('\u001b[<0;2;2M\u001b[<0;2;2m');
  });

  test('routes ordinary mobile editing through the native textarea', () => {
    const key = {
      type: 'keydown',
      key: 'a',
      altKey: false,
      ctrlKey: false,
      metaKey: false,
      isComposing: false,
    };
    expect(usesNativeMobileTextInput(key)).toBe(true);
    expect(usesNativeMobileTextInput({ ...key, key: 'Backspace' })).toBe(true);
    expect(usesNativeMobileTextInput({ ...key, type: 'keyup' })).toBe(false);
    expect(usesNativeMobileTextInput({ ...key, ctrlKey: true })).toBe(false);
    expect(usesNativeMobileTextInput({ ...key, key: 'Enter' })).toBe(false);
  });

  test('reconciles the native double-space period replacement', () => {
    const terminalValue = applyTerminalInput('hello ', ' ');
    const correction = correctionForTerminalInput(terminalValue, 'hello. ');

    expect(correction).toBe('\u007f\u007f. ');
    expect(applyTerminalInput(terminalValue, correction)).toBe('hello. ');
  });

  test('reconciles autocorrected words and ignores terminal control sequences', () => {
    expect(correctionForTerminalInput('teh ', 'the ')).toBe(
      '\u007f\u007f\u007fhe ',
    );
    expect(applyTerminalInput('prompt', '\u001b[D')).toBe('prompt');
    expect(applyTerminalInput('prompt', '\r')).toBe('');
  });
});
