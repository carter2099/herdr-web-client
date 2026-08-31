export const MAX_CLIPBOARD_TEXT_BYTES = 1024 * 1024;

const MAX_BASE64_LENGTH = Math.ceil(MAX_CLIPBOARD_TEXT_BYTES / 3) * 4;
const BASE64_PATTERN =
  /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
const utf8Decoder = new TextDecoder('utf-8', { fatal: true });

export function decodeOsc52Text(data) {
  const separator = data.indexOf(';');
  if (separator === -1 || data.slice(0, separator) !== 'c') {
    return null;
  }

  const encoded = data.slice(separator + 1);
  if (encoded.length > MAX_BASE64_LENGTH || !BASE64_PATTERN.test(encoded)) {
    return null;
  }

  try {
    const binary = atob(encoded);
    if (binary.length > MAX_CLIPBOARD_TEXT_BYTES) {
      return null;
    }
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }
    return utf8Decoder.decode(bytes);
  } catch {
    return null;
  }
}

export function handleOsc52Clipboard(data, writeText) {
  const text = decodeOsc52Text(data);
  if (text === null) {
    return false;
  }
  writeText(text);
  return true;
}
