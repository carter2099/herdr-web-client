import { describe, expect, test } from 'bun:test';
import {
  decodeOsc52Text,
  handleOsc52Clipboard,
  MAX_CLIPBOARD_TEXT_BYTES,
} from './clipboard.js';

const base64 = (text) => Buffer.from(text, 'utf8').toString('base64');

describe('OSC 52 clipboard bridge', () => {
  test('dispatches Herdr copy-on-select text to the clipboard writer', () => {
    let written;
    const handled = handleOsc52Clipboard(
      `c;${base64('alpha\nβeta 🙂')}`,
      (text) => {
        written = text;
      },
    );

    expect(handled).toBe(true);
    expect(written).toBe('alpha\nβeta 🙂');
  });

  test('accepts an empty clipboard write', () => {
    expect(decodeOsc52Text('c;')).toBe('');
  });

  test.each([
    ['read request', 'c;?'],
    ['non-clipboard target', `p;${base64('text')}`],
    ['missing target separator', base64('text')],
    ['non-canonical base64', 'c;YQ'],
    ['invalid base64', 'c;%%%'],
    ['invalid UTF-8', 'c;//4='],
  ])('rejects %s', (_name, data) => {
    let called = false;
    expect(
      handleOsc52Clipboard(data, () => {
        called = true;
      }),
    ).toBe(false);
    expect(called).toBe(false);
  });

  test('rejects text larger than the bridge output limit', () => {
    const oversized = Buffer.alloc(MAX_CLIPBOARD_TEXT_BYTES + 1, 0x61).toString(
      'base64',
    );
    expect(decodeOsc52Text(`c;${oversized}`)).toBeNull();
  });
});
