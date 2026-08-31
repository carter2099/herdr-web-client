export const SHIFT_ENTER_SEQUENCE = '\u001b[13;2u';

export function shiftEnterAction(event) {
  const isShiftEnter =
    event.key === 'Enter' &&
    event.shiftKey &&
    !event.altKey &&
    !event.ctrlKey &&
    !event.metaKey &&
    !event.isComposing;
  if (!isShiftEnter) {
    return null;
  }
  if (event.type === 'keydown') {
    return 'send';
  }
  if (event.type === 'keypress') {
    return 'suppress';
  }
  return null;
}
