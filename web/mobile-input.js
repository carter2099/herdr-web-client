export function configureNaturalTextInput(input) {
  input.setAttribute('autocomplete', 'on');
  input.setAttribute('autocapitalize', 'sentences');
  input.setAttribute('autocorrect', 'on');
  input.setAttribute('spellcheck', 'true');
  input.setAttribute('inputmode', 'text');
}

export function mobileSwitcherMouseSequence(columns) {
  const terminalColumns = Number.isFinite(columns)
    ? Math.max(1, Math.floor(columns))
    : 1;
  const switchWidth = Math.min(10, terminalColumns);
  const switchColumn =
    terminalColumns - switchWidth + Math.min(1, switchWidth - 1) + 1;
  return `\u001b[<0;${switchColumn};2M\u001b[<0;${switchColumn};2m`;
}

export function terminalTypingActive(ready, focused, desktop, enabled) {
  return ready && focused && (desktop || enabled);
}

export function usesNativeMobileTextInput(event) {
  return (
    (event.type === 'keydown' || event.type === 'keypress') &&
    !event.altKey &&
    !event.ctrlKey &&
    !event.metaKey &&
    !event.isComposing &&
    (event.key.length === 1 ||
      event.key === 'Backspace' ||
      event.key === 'Delete')
  );
}

export function applyTerminalInput(value, data) {
  if (
    data.includes('\u001b') ||
    [...data].some((character) => {
      const code = character.codePointAt(0);
      return code < 0x20 && character !== '\r';
    })
  ) {
    return value;
  }
  const result = [...value];
  for (const character of data) {
    if (character === '\r') {
      result.length = 0;
    } else if (character === '\u007f') {
      result.pop();
    } else {
      result.push(character);
    }
  }
  return result.join('');
}

export function correctionForTerminalInput(current, target) {
  if (current === target) {
    return '';
  }
  const currentCharacters = [...current];
  const targetCharacters = [...target];
  let prefixLength = 0;
  while (
    prefixLength < currentCharacters.length &&
    prefixLength < targetCharacters.length &&
    currentCharacters[prefixLength] === targetCharacters[prefixLength]
  ) {
    prefixLength += 1;
  }
  return (
    '\u007f'.repeat(currentCharacters.length - prefixLength) +
    targetCharacters.slice(prefixLength).join('')
  );
}
