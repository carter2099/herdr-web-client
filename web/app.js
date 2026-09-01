import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { handleOsc52Clipboard } from './clipboard.js';
import { playCompletionPing } from './completion-audio.js';
import {
  applyTerminalInput,
  configureNaturalTextInput,
  correctionForTerminalInput,
  mobileSwitcherMouseSequence,
  terminalTypingActive,
  usesNativeMobileTextInput,
} from './mobile-input.js';
import { SHIFT_ENTER_SEQUENCE, shiftEnterAction } from './terminal-input.js';
import '@xterm/xterm/css/xterm.css';
import './styles.css';

const ATTACH_PROTOCOL = 'herdr-web-client.v1';
const SESSION_PATH = '/api/session';
const ATTACH_PATH = '/api/attach';
const ACTIVE_ATTACHMENT_MESSAGE = 'another attachment is already active';
const MIN_COLS = 20;
const MAX_COLS = 400;
const MIN_ROWS = 5;
const MAX_ROWS = 200;
const MAX_INPUT_FRAME_BYTES = 60 * 1024;
const MAX_PENDING_OUTPUT_BYTES = 1024 * 1024;
const FIT_DEBOUNCE_MS = 80;
const HANDSHAKE_TIMEOUT_MS = 15_000;
const RECONNECT_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 15_000];
const COMPLETION_TOAST_MS = 6_000;
const TOUCH_SCROLL_THRESHOLD_PX = 6;

const textEncoder = new TextEncoder();
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const desktopPointer = window.matchMedia(
  '(min-width: 48rem) and (pointer: fine)',
);
const CompletionAudioContext = window.AudioContext || window.webkitAudioContext;

// Load one patched monospace face for text and symbols before xterm measures the grid.
// A symbol-only fallback has wider advances than xterm's text cells and gets clipped.
if (document.fonts?.load) {
  try {
    await document.fonts.load(
      '14px "Herdr Terminal Mono"',
      '0\ue0b0\uf14a\udb84\udeb7',
    );
  } catch {
    // Keep the terminal usable with its ordinary monospace fallbacks if the font request fails.
  }
}

function elementById(id) {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element;
}

const root = document.documentElement;
const app = elementById('app');
const terminalStage = elementById('terminal-stage');
const terminalElement = elementById('terminal');
const statusLabel = elementById('status-label');
const connectionPanel = elementById('connection-panel');
const connectionTitle = elementById('connection-title');
const connectionDetail = elementById('connection-detail');
const connectionSpinner = elementById('connection-spinner');
const connectionAction = elementById('connection-action');
const typeButton = elementById('type-button');
const mobileSwitchMask = elementById('mobile-switch-mask');
const keysSheet = elementById('keys-sheet');
const herdrSheet = elementById('herdr-sheet');
const screenReaderButton = elementById('screen-reader-button');
const announcer = elementById('announcer');
const completionToast = elementById('completion-toast');
const completionToastLabel = elementById('completion-toast-label');
const completionToastDetail = elementById('completion-toast-detail');

const styleTokens = getComputedStyle(root);
const token = (name) => styleTokens.getPropertyValue(name).trim();
const numberToken = (name, fallback) => {
  const value = Number.parseFloat(token(name));
  return Number.isFinite(value) ? value : fallback;
};

const terminal = new Terminal({
  allowProposedApi: false,
  allowTransparency: false,
  altClickMovesCursor: true,
  convertEol: false,
  cursorBlink: !reducedMotion.matches,
  cursorInactiveStyle: 'outline',
  cursorStyle: 'block',
  disableStdin: true,
  drawBoldTextInBrightColors: true,
  fontFamily: token('--font-terminal'),
  fontSize: numberToken('--terminal-font-size', 14),
  fontWeight: 'normal',
  fontWeightBold: '600',
  letterSpacing: numberToken('--terminal-letter-spacing', 0),
  lineHeight: numberToken('--terminal-line-height', 1.2),
  linkHandler: null,
  minimumContrastRatio: 4.5,
  rightClickSelectsWord: true,
  screenReaderMode: false,
  scrollback: 5_000,
  smoothScrollDuration: reducedMotion.matches ? 0 : 80,
  theme: {
    background: token('--color-terminal'),
    foreground: token('--color-text'),
    cursor: token('--color-accent'),
    cursorAccent: token('--color-on-accent'),
    selectionBackground: token('--color-selection'),
    selectionInactiveBackground: token('--color-border'),
    black: token('--color-ansi-black'),
    red: token('--color-danger'),
    green: token('--color-success'),
    yellow: token('--color-warning'),
    blue: token('--color-accent'),
    magenta: token('--color-magenta'),
    cyan: token('--color-cyan'),
    white: token('--color-text'),
    brightBlack: token('--color-ansi-bright-black'),
    brightRed: token('--color-danger'),
    brightGreen: token('--color-success'),
    brightYellow: token('--color-warning'),
    brightBlue: token('--color-accent'),
    brightMagenta: token('--color-magenta'),
    brightCyan: token('--color-cyan'),
    brightWhite: token('--color-text-strong'),
  },
});

const fitAddon = new FitAddon();
terminal.loadAddon(fitAddon);
terminal.open(terminalElement);

// Herdr forwards copy-on-select through OSC 52. Keep hyperlink OSC disabled,
// but bridge clipboard writes to the browser that owns this terminal.
terminal.parser.registerOscHandler(52, (data) => {
  handleOsc52Clipboard(data, (text) => {
    void writeClipboardText(text);
  });
  return true;
});
terminal.parser.registerOscHandler(8, () => true);

const terminalTextarea = terminalElement.querySelector('textarea');
if (terminalTextarea) {
  terminalTextarea.setAttribute('aria-label', 'Terminal input');
  terminalTextarea.setAttribute('aria-describedby', 'terminal-help');
  configureNaturalTextInput(terminalTextarea);
  terminalTextarea.setAttribute('enterkeyhint', 'send');
}

const TERMINAL_KEYS = new Map([
  ['escape', Uint8Array.of(0x1b)],
  ['tab', Uint8Array.of(0x09)],
  ['shift-tab', Uint8Array.of(0x1b, 0x5b, 0x5a)],
  ['arrow-up', Uint8Array.of(0x1b, 0x5b, 0x41)],
  ['arrow-down', Uint8Array.of(0x1b, 0x5b, 0x42)],
  ['arrow-right', Uint8Array.of(0x1b, 0x5b, 0x43)],
  ['arrow-left', Uint8Array.of(0x1b, 0x5b, 0x44)],
  ['home', Uint8Array.of(0x1b, 0x5b, 0x48)],
  ['end', Uint8Array.of(0x1b, 0x5b, 0x46)],
  ['page-up', Uint8Array.of(0x1b, 0x5b, 0x35, 0x7e)],
  ['page-down', Uint8Array.of(0x1b, 0x5b, 0x36, 0x7e)],
  ['backspace', Uint8Array.of(0x7f)],
  ['delete', Uint8Array.of(0x1b, 0x5b, 0x33, 0x7e)],
  ['enter', Uint8Array.of(0x0d)],
  ['ctrl-c', Uint8Array.of(0x03)],
  ['ctrl-d', Uint8Array.of(0x04)],
  ['ctrl-z', Uint8Array.of(0x1a)],
  ['ctrl-l', Uint8Array.of(0x0c)],
]);

const APPLICATION_TERMINAL_KEYS = new Map([
  ['arrow-up', Uint8Array.of(0x1b, 0x4f, 0x41)],
  ['arrow-down', Uint8Array.of(0x1b, 0x4f, 0x42)],
  ['arrow-right', Uint8Array.of(0x1b, 0x4f, 0x43)],
  ['arrow-left', Uint8Array.of(0x1b, 0x4f, 0x44)],
  ['home', Uint8Array.of(0x1b, 0x4f, 0x48)],
  ['end', Uint8Array.of(0x1b, 0x4f, 0x46)],
]);

function bytesForTerminalKey(name) {
  if (terminal.modes?.applicationCursorKeysMode) {
    const applicationSequence = APPLICATION_TERMINAL_KEYS.get(name);
    if (applicationSequence) {
      return applicationSequence;
    }
  }
  return TERMINAL_KEYS.get(name);
}

const TERMINAL_KEY_NAMES = new Map([
  ['escape', 'Escape'],
  ['tab', 'Tab'],
  ['shift-tab', 'Shift Tab'],
  ['arrow-up', 'Up arrow'],
  ['arrow-down', 'Down arrow'],
  ['arrow-right', 'Right arrow'],
  ['arrow-left', 'Left arrow'],
  ['home', 'Home'],
  ['end', 'End'],
  ['page-up', 'Page up'],
  ['page-down', 'Page down'],
  ['backspace', 'Backspace'],
  ['delete', 'Delete'],
  ['enter', 'Enter'],
  ['ctrl-c', 'Control C'],
  ['ctrl-d', 'Control D'],
  ['ctrl-z', 'Control Z'],
  ['ctrl-l', 'Control L'],
]);

let connectionState = 'connecting';
let socket = null;
let connectionGeneration = 0;
let sessionController = null;
let handshakeTimer = null;
let reconnectTimer = null;
let reconnectCountdownTimer = null;
let reconnectAttempt = 0;
let fitTimer = null;
let viewportFrame = null;
let pageSuspended = false;
let lastSentDimensions = null;
let pastePending = false;
let activeSheet = null;
const sheetTriggers = new WeakMap();
const sheetFocusReturns = new WeakMap();
let hasFocusedDesktopTerminal = false;
let restoreDesktopTerminalFocus = false;
let mobileKeyboardEnabled = false;
let mobileTerminalValue = '';
let mobileTerminalIsComposing = false;
let terminalPointerGesture = null;
let announcementRevision = 0;
let completionAudioContext = null;
let completionToastTimer = null;

function clampDimension(value, minimum, maximum) {
  const integer = Number.isFinite(value) ? Math.floor(value) : minimum;
  return Math.max(minimum, Math.min(maximum, integer));
}

function currentDimensions() {
  const dimensions = {
    cols: clampDimension(terminal.cols, MIN_COLS, MAX_COLS),
    rows: clampDimension(terminal.rows, MIN_ROWS, MAX_ROWS),
  };
  if (terminal.cols !== dimensions.cols || terminal.rows !== dimensions.rows) {
    terminal.resize(dimensions.cols, dimensions.rows);
  }
  return dimensions;
}

function sameDimensions(left, right) {
  return Boolean(
    left && right && left.cols === right.cols && left.rows === right.rows,
  );
}

function conciseMessage(message) {
  const value = typeof message === 'string' ? message.trim() : '';
  if (!value) {
    return 'The server could not start the terminal.';
  }
  const maximumLength = 240;
  return value.length > maximumLength
    ? `${value.slice(0, maximumLength - 1)}…`
    : value;
}

function announce(message) {
  const revision = ++announcementRevision;
  announcer.textContent = '';
  window.requestAnimationFrame(() => {
    if (revision === announcementRevision) {
      announcer.textContent = message;
    }
  });
}

function stopCompletionAudioUnlock() {
  document.removeEventListener('pointerdown', unlockCompletionAudio, true);
  document.removeEventListener('keydown', unlockCompletionAudio, true);
}

function unlockCompletionAudio() {
  if (!CompletionAudioContext) {
    stopCompletionAudioUnlock();
    return;
  }
  try {
    completionAudioContext ||= new CompletionAudioContext();
    if (completionAudioContext.state === 'running') {
      stopCompletionAudioUnlock();
      return;
    }
    void completionAudioContext
      .resume()
      .then(() => {
        if (completionAudioContext?.state === 'running') {
          stopCompletionAudioUnlock();
        }
      })
      .catch(() => {
        // Keep the listeners so a later trusted gesture can try again.
      });
  } catch {
    stopCompletionAudioUnlock();
  }
}

function showAgentCompletion(message) {
  const agent = typeof message.agent === 'string' ? message.agent.trim() : '';
  const title = typeof message.title === 'string' ? message.title.trim() : '';
  completionToastLabel.textContent = agent
    ? `${agent.toUpperCase()} completed`
    : 'Agent completed';
  completionToastDetail.textContent = title;
  completionToastDetail.hidden = title.length === 0;
  completionToast.hidden = false;
  window.clearTimeout(completionToastTimer);
  completionToastTimer = window.setTimeout(() => {
    completionToast.hidden = true;
  }, COMPLETION_TOAST_MS);
  playCompletionPing(completionAudioContext);
}

document.addEventListener('pointerdown', unlockCompletionAudio, {
  capture: true,
  passive: true,
});
document.addEventListener('keydown', unlockCompletionAudio, { capture: true });

async function writeClipboardText(text) {
  if (!navigator.clipboard?.writeText) {
    announce('Automatic clipboard copy is not available in this browser.');
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    announce('Copied to clipboard.');
  } catch {
    announce('The browser blocked automatic clipboard access.');
  }
}

function stateView(state, context = {}) {
  switch (state) {
    case 'connecting':
      return {
        status: 'Connecting',
        title: 'Connecting to Herdr',
        detail: context.detail || 'Starting the terminal session…',
        busy: true,
      };
    case 'reconnecting': {
      const seconds = context.seconds || 1;
      return {
        status: 'Reconnecting',
        title: 'Reconnecting',
        detail: `Connection lost. Trying again in ${seconds} ${seconds === 1 ? 'second' : 'seconds'}. Keystrokes are not queued.`,
        action: 'Try now',
        busy: true,
      };
    }
    case 'ready':
      return {
        status: 'Connected',
        title: '',
        detail: '',
      };
    case 'limited':
      return {
        status: 'Attachment busy',
        title: 'Herdr is already attached',
        detail:
          'Only one browser attachment can be active. Close the other attachment, then try again.',
        action: 'Try again',
      };
    case 'offline':
      return {
        status: 'Offline',
        title: 'You’re offline',
        detail: 'Keystrokes are not queued.',
        action: 'Check again',
      };
    case 'ended':
      return {
        status: 'Session ended',
        title: 'Herdr exited',
        detail: Number.isInteger(context.code)
          ? `The terminal process ended with code ${context.code}.`
          : 'The terminal process ended.',
        action: 'Start again',
      };
    default:
      return {
        status: 'Connection error',
        title: 'Couldn’t connect',
        detail:
          context.detail || 'The terminal connection could not be established.',
        action: 'Try again',
      };
  }
}

function terminalHasFocus() {
  return Boolean(
    document.activeElement && terminalElement.contains(document.activeElement),
  );
}
function beginTerminalPointer(event) {
  if (
    desktopPointer.matches ||
    event.pointerType !== 'touch' ||
    !event.isPrimary
  ) {
    terminalPointerGesture = null;
    return;
  }
  terminalPointerGesture = {
    pointerId: event.pointerId,
    initialX: event.clientX,
    initialY: event.clientY,
    lastY: event.clientY,
    scrolling: false,
  };
}

function moveTerminalPointer(event) {
  if (
    desktopPointer.matches ||
    !terminalPointerGesture ||
    event.pointerType !== 'touch' ||
    event.pointerId !== terminalPointerGesture.pointerId
  ) {
    return;
  }

  if (!terminalPointerGesture.scrolling) {
    const horizontalTravel = event.clientX - terminalPointerGesture.initialX;
    const verticalTravel = event.clientY - terminalPointerGesture.initialY;
    if (Math.abs(verticalTravel) < TOUCH_SCROLL_THRESHOLD_PX) {
      return;
    }
    if (Math.abs(verticalTravel) <= Math.abs(horizontalTravel)) {
      terminalPointerGesture = null;
      return;
    }
    terminalPointerGesture.scrolling = true;
  }

  const deltaY = terminalPointerGesture.lastY - event.clientY;
  terminalPointerGesture.lastY = event.clientY;
  event.preventDefault();
  event.stopPropagation();
  if (deltaY === 0) {
    return;
  }

  const screen = terminalElement.querySelector('.xterm-screen');
  if (!(screen instanceof HTMLElement)) {
    return;
  }
  // xterm 6.0's virtual viewport handles wheel input but does not forward
  // touch gestures. Reuse its wheel path so scrollback, alternate buffers,
  // and terminal mouse reporting retain xterm's native behavior.
  const wheelEvent = new WheelEvent('wheel', {
    bubbles: true,
    cancelable: true,
    clientX: event.clientX,
    clientY: event.clientY,
    deltaMode: WheelEvent.DOM_DELTA_PIXEL,
    deltaY,
    view: window,
  });
  // xterm's wheel normalizer checks the legacy field first. Constructed wheel
  // events expose it as zero unless the matching value is supplied explicitly.
  Object.defineProperty(wheelEvent, 'wheelDeltaY', { value: -deltaY });
  screen.dispatchEvent(wheelEvent);
}

function endTerminalPointer(event) {
  if (event.pointerId === terminalPointerGesture?.pointerId) {
    terminalPointerGesture = null;
  }
}

function syncMobileSwitcherMask() {
  if (desktopPointer.matches || terminal.cols < 1 || terminal.rows < 2) {
    mobileSwitchMask.hidden = true;
    return;
  }
  const screen = terminalElement.querySelector('.xterm-screen');
  if (!(screen instanceof HTMLElement)) {
    mobileSwitchMask.hidden = true;
    return;
  }
  const screenRect = screen.getBoundingClientRect();
  const stageRect = terminalStage.getBoundingClientRect();
  if (screenRect.width <= 0 || screenRect.height <= 0) {
    mobileSwitchMask.hidden = true;
    return;
  }

  // Herdr v0.8.2 reserves the rightmost 10 columns of its two-row mobile header
  // for Switch. Keep its hit area intact, but cover it after moving the control
  // to the app header.
  const switchWidth = Math.min(10, terminal.cols);
  const cellWidth = screenRect.width / terminal.cols;
  const cellHeight = screenRect.height / terminal.rows;
  mobileSwitchMask.style.left = `${Math.floor(screenRect.left - stageRect.left + (terminal.cols - switchWidth) * cellWidth)}px`;
  mobileSwitchMask.style.top = `${Math.floor(screenRect.top - stageRect.top)}px`;
  mobileSwitchMask.style.width = `${Math.ceil(switchWidth * cellWidth) + 1}px`;
  mobileSwitchMask.style.height = `${Math.ceil(2 * cellHeight) + 1}px`;
  mobileSwitchMask.hidden = false;
}

function syncTypeButton() {
  const typing = terminalTypingActive(
    connectionState === 'ready',
    terminalHasFocus(),
    desktopPointer.matches,
    mobileKeyboardEnabled,
  );
  typeButton.textContent = typing ? 'Done' : 'Type';
  typeButton.setAttribute('aria-pressed', String(typing));
  typeButton.setAttribute(
    'aria-label',
    typing ? 'Done typing' : 'Type in terminal',
  );
}

function syncScreenReaderButton() {
  const enabled = terminal.options.screenReaderMode === true;
  screenReaderButton.textContent = `Screen reader: ${enabled ? 'on' : 'off'}`;
  screenReaderButton.setAttribute('aria-pressed', String(enabled));
}

function toggleScreenReaderMode() {
  terminal.options.screenReaderMode =
    terminal.options.screenReaderMode !== true;
  syncScreenReaderButton();
  announce(
    `Screen reader mode ${terminal.options.screenReaderMode ? 'on' : 'off'}.`,
  );
}

function closeTerminalKeyboard() {
  mobileKeyboardEnabled = false;
  if (terminalHasFocus()) {
    terminal.blur();
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  }
  syncTypeButton();
}

function isTransportReady() {
  return connectionState === 'ready' && socket?.readyState === WebSocket.OPEN;
}

function syncConnectionControls() {
  const ready = isTransportReady();
  const connectionRequired = document.querySelectorAll(
    '[data-action="toggle-keyboard"], [data-action="toggle-switcher"], [data-action="open-keys"], [data-action="open-herdr"], [data-action="paste"], [data-terminal-key], [data-herdr-key]',
  );

  for (const control of connectionRequired) {
    const isPasteControl = control.matches('[data-action="paste"]');
    control.disabled = !ready || (isPasteControl && pastePending);
  }

  for (const pasteButton of document.querySelectorAll(
    '[data-action="paste"]',
  )) {
    pasteButton.setAttribute('aria-busy', String(pastePending));
    pasteButton.textContent = pastePending ? 'Reading…' : 'Paste';
  }

  if (!ready) {
    closeTerminalKeyboard();
    if (activeSheet === keysSheet || activeSheet === herdrSheet) {
      closeSheet(activeSheet, false);
    }
  }

  syncTypeButton();
}

function setConnectionState(state, context = {}) {
  const previousState = connectionState;
  connectionState = state;
  const view = stateView(state, context);

  app.dataset.connection = state;
  statusLabel.textContent = view.status;
  connectionPanel.hidden = state === 'ready';
  connectionSpinner.hidden = !view.busy;
  connectionTitle.textContent = view.title;
  connectionDetail.textContent = view.detail;
  connectionAction.hidden = !view.action;
  connectionAction.textContent = view.action || '';
  terminal.options.disableStdin = state !== 'ready';

  syncConnectionControls();
  if (state !== previousState) {
    announce(state === 'ready' ? view.status : `${view.title}. ${view.detail}`);
  }
}

function setSheetExpanded(sheet, expanded) {
  for (const control of document.querySelectorAll(
    `[aria-controls="${sheet.id}"]`,
  )) {
    control.setAttribute('aria-expanded', String(expanded));
  }
}

function openSheet(sheet, trigger, focusTarget) {
  if (activeSheet?.open) {
    if (activeSheet === sheet) {
      closeSheet(sheet);
      return false;
    }
    closeSheet(activeSheet, false);
  }

  activeSheet = sheet;
  sheetTriggers.set(sheet, trigger);
  sheetFocusReturns.set(sheet, true);
  setSheetExpanded(sheet, true);

  if (desktopPointer.matches && typeof sheet.show === 'function') {
    sheet.show();
  } else if (typeof sheet.showModal === 'function') {
    sheet.showModal();
  } else {
    sheet.setAttribute('open', '');
  }

  const target = focusTarget || sheet.querySelector('button, textarea');
  target?.focus({ preventScroll: true });
  return true;
}

function closeSheet(sheet, returnFocus = true) {
  if (!sheet.open) {
    return;
  }
  sheetFocusReturns.set(sheet, returnFocus);
  if (typeof sheet.close === 'function') {
    sheet.close();
  } else {
    sheet.removeAttribute('open');
    sheet.dispatchEvent(new Event('close'));
  }
}

for (const sheet of [keysSheet, herdrSheet]) {
  sheet.addEventListener('cancel', (event) => {
    event.preventDefault();
    closeSheet(sheet);
  });

  sheet.addEventListener('close', () => {
    const trigger = sheetTriggers.get(sheet);
    const shouldReturnFocus = sheetFocusReturns.get(sheet) !== false;
    sheetTriggers.delete(sheet);
    sheetFocusReturns.delete(sheet);
    setSheetExpanded(sheet, false);
    if (activeSheet === sheet) {
      activeSheet = null;
    }
    if (shouldReturnFocus && trigger?.isConnected && !trigger.disabled) {
      trigger.focus({ preventScroll: true });
    }
  });
}

document.addEventListener('pointerdown', (event) => {
  if (
    !desktopPointer.matches ||
    !activeSheet?.open ||
    !(event.target instanceof Element)
  ) {
    return;
  }
  if (activeSheet.contains(event.target)) {
    return;
  }
  const relatedControl = event.target.closest(
    `[aria-controls="${activeSheet.id}"]`,
  );
  if (!relatedControl) {
    closeSheet(activeSheet, false);
  }
});

document.addEventListener('keydown', (event) => {
  if (
    event.key === 'Escape' &&
    !event.isComposing &&
    activeSheet?.open &&
    desktopPointer.matches
  ) {
    event.preventDefault();
    closeSheet(activeSheet);
  }
});

function clearReconnectTimers() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (reconnectCountdownTimer !== null) {
    window.clearInterval(reconnectCountdownTimer);
    reconnectCountdownTimer = null;
  }
}

function clearHandshakeTimer() {
  if (handshakeTimer !== null) {
    window.clearTimeout(handshakeTimer);
    handshakeTimer = null;
  }
}

function stopCurrentTransport(reason) {
  if (connectionState === 'ready' && terminalHasFocus()) {
    restoreDesktopTerminalFocus = true;
  }
  connectionGeneration += 1;
  sessionController?.abort();
  sessionController = null;
  clearHandshakeTimer();

  const currentSocket = socket;
  socket = null;
  if (currentSocket && currentSocket.readyState < WebSocket.CLOSING) {
    try {
      currentSocket.close(1000, reason);
    } catch {
      // The guarded event handlers will ignore this obsolete transport.
    }
  }
}

function scheduleReconnect() {
  if (connectionState === 'ready' && terminalHasFocus()) {
    restoreDesktopTerminalFocus = true;
  }
  clearReconnectTimers();

  if (pageSuspended) {
    return;
  }
  if (!navigator.onLine) {
    setConnectionState('offline');
    return;
  }

  const index = Math.min(reconnectAttempt, RECONNECT_DELAYS_MS.length - 1);
  const delay = RECONNECT_DELAYS_MS[index];
  reconnectAttempt += 1;
  const reconnectAt = Date.now() + delay;

  const renderCountdown = () => {
    const remaining = Math.max(
      1,
      Math.ceil((reconnectAt - Date.now()) / 1_000),
    );
    setConnectionState('reconnecting', { seconds: remaining });
  };

  renderCountdown();
  reconnectCountdownTimer = window.setInterval(renderCountdown, 1_000);
  reconnectTimer = window.setTimeout(() => {
    clearReconnectTimers();
    void beginConnection({ automatic: true });
  }, delay);
}

function failAndReconnect() {
  stopCurrentTransport('connection lost');
  scheduleReconnect();
}

function sendBytes(bytes, announceFailure = true) {
  if (!isTransportReady()) {
    if (announceFailure) {
      announce('Not sent. Herdr is disconnected.');
    }
    return false;
  }

  if (bytes.byteLength === 0) {
    return true;
  }

  const activeSocket = socket;
  try {
    for (
      let offset = 0;
      offset < bytes.byteLength;
      offset += MAX_INPUT_FRAME_BYTES
    ) {
      activeSocket.send(bytes.subarray(offset, offset + MAX_INPUT_FRAME_BYTES));
    }
    return true;
  } catch {
    if (announceFailure) {
      announce('Input was not sent. Reconnecting.');
    }
    failAndReconnect();
    return false;
  }
}

function sendText(text, announceFailure = true) {
  return sendBytes(textEncoder.encode(text), announceFailure);
}

function pasteIntoTerminal(text) {
  if (!isTransportReady()) {
    announce('Not sent. Herdr is disconnected.');
    return false;
  }
  if (!text) {
    return true;
  }

  try {
    terminal.paste(text);
    return isTransportReady();
  } catch {
    announce('Text could not be inserted.');
    return false;
  }
}

function reconcileMobileTerminalInput() {
  if (
    !mobileKeyboardEnabled ||
    !terminalTextarea ||
    mobileTerminalIsComposing
  ) {
    return;
  }
  const target = terminalTextarea.value;
  const correction = correctionForTerminalInput(mobileTerminalValue, target);
  if (correction && !sendText(correction)) {
    return;
  }
  mobileTerminalValue = target;
}

terminal.attachCustomKeyEventHandler((event) => {
  const action = shiftEnterAction(event);
  if (action !== null) {
    event.preventDefault();
    event.stopPropagation();
    if (action === 'send') {
      sendText(SHIFT_ENTER_SEQUENCE);
    }
    return false;
  }
  return !(mobileKeyboardEnabled && usesNativeMobileTextInput(event));
});

terminal.onData((data) => {
  if (mobileKeyboardEnabled) {
    mobileTerminalValue = applyTerminalInput(mobileTerminalValue, data);
  }
  sendText(data);
});

terminal.onBinary((data) => {
  const bytes = new Uint8Array(data.length);
  for (let index = 0; index < data.length; index += 1) {
    bytes[index] = data.charCodeAt(index) & 0xff;
  }
  sendBytes(bytes);
});

terminalTextarea?.addEventListener('compositionstart', () => {
  mobileTerminalIsComposing = true;
});

terminalTextarea?.addEventListener('compositionend', () => {
  mobileTerminalIsComposing = false;
  window.queueMicrotask(reconcileMobileTerminalInput);
});

terminalElement.addEventListener(
  'input',
  (event) => {
    if (!mobileKeyboardEnabled || event.target !== terminalTextarea) {
      return;
    }
    event.stopPropagation();
    if (!event.isComposing) {
      reconcileMobileTerminalInput();
    }
  },
  true,
);

function sendResize() {
  if (!isTransportReady()) {
    return;
  }
  const dimensions = currentDimensions();
  if (sameDimensions(dimensions, lastSentDimensions)) {
    return;
  }

  try {
    socket.send(JSON.stringify({ type: 'resize', ...dimensions }));
    lastSentDimensions = dimensions;
  } catch {
    failAndReconnect();
  }
}

terminal.onResize(() => {
  syncMobileSwitcherMask();
  sendResize();
});

function fitTerminal() {
  fitTimer = null;
  if (
    !terminalElement.isConnected ||
    terminalStage.clientWidth === 0 ||
    terminalStage.clientHeight === 0
  ) {
    return;
  }
  try {
    fitAddon.fit();
    syncMobileSwitcherMask();
  } catch {
    // A later ResizeObserver or VisualViewport event retries once layout is stable.
  }
}

function scheduleFit(immediate = false) {
  if (fitTimer !== null) {
    window.clearTimeout(fitTimer);
  }
  fitTimer = window.setTimeout(fitTerminal, immediate ? 0 : FIT_DEBOUNCE_MS);
}

function syncVisualViewport() {
  if (viewportFrame !== null) {
    return;
  }
  viewportFrame = window.requestAnimationFrame(() => {
    viewportFrame = null;
    const viewport = window.visualViewport;
    const height = viewport?.height || window.innerHeight;
    const top = viewport?.offsetTop || 0;
    const bottom = Math.max(0, root.clientHeight - height - top);
    root.style.setProperty('--visual-viewport-height', `${height}px`);
    root.style.setProperty('--visual-viewport-top', `${top}px`);
    root.style.setProperty('--visual-viewport-bottom', `${bottom}px`);
    scheduleFit();
  });
}

window.addEventListener('resize', syncVisualViewport, { passive: true });
window.visualViewport?.addEventListener('resize', syncVisualViewport, {
  passive: true,
});
window.visualViewport?.addEventListener('scroll', syncVisualViewport, {
  passive: true,
});

if ('ResizeObserver' in window) {
  const terminalResizeObserver = new ResizeObserver(() => scheduleFit());
  terminalResizeObserver.observe(terminalStage);
}

class AttachmentLimitError extends Error {}

async function requestSession(signal) {
  let response;
  try {
    response = await fetch(SESSION_PATH, {
      method: 'GET',
      mode: 'same-origin',
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'manual',
      headers: {
        Accept: 'application/json',
        'X-Herdr-Web-Client-Request': 'session',
      },
      signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error;
    }
    throw new Error('The session request failed.');
  }

  if (
    response.type === 'opaqueredirect' ||
    (response.status >= 300 && response.status < 400)
  ) {
    throw new Error('The session request was redirected.');
  }
  if (response.status === 409) {
    throw new AttachmentLimitError(ACTIVE_ATTACHMENT_MESSAGE);
  }
  if (!response.ok) {
    throw new Error(`The session request returned ${response.status}.`);
  }

  const responseOrigin = new URL(response.url, window.location.href).origin;
  if (response.redirected && responseOrigin !== window.location.origin) {
    throw new Error('The session request was redirected.');
  }

  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new Error('The session response was not available.');
  }

  const validPayload =
    payload &&
    typeof payload === 'object' &&
    typeof payload.nonce === 'string' &&
    payload.nonce.length > 0 &&
    typeof payload.expires_at === 'string' &&
    Number.isFinite(Date.parse(payload.expires_at));

  if (!validPayload) {
    throw new Error('The session response was invalid.');
  }

  return { nonce: payload.nonce };
}

function attachURL() {
  const url = new URL(ATTACH_PATH, window.location.href);
  if (url.protocol === 'https:') {
    url.protocol = 'wss:';
  } else if (url.protocol === 'http:') {
    url.protocol = 'ws:';
  } else {
    throw new Error('Herdr requires an HTTP or HTTPS origin.');
  }
  return url.href;
}

function protocolFailure(context, websocket, detail) {
  context.finalState = { state: 'error', detail };
  setConnectionState('error', { detail });
  try {
    websocket.close(1002, 'invalid server message');
  } catch {
    // The final protocol-error state is already visible.
  }
}

function handleServerControl(context, websocket, data) {
  let message;
  try {
    message = JSON.parse(data);
  } catch {
    protocolFailure(
      context,
      websocket,
      'The server sent invalid control data.',
    );
    return;
  }

  if (
    !message ||
    typeof message !== 'object' ||
    Array.isArray(message) ||
    typeof message.type !== 'string'
  ) {
    protocolFailure(
      context,
      websocket,
      'The server sent an invalid control message.',
    );
    return;
  }

  if (context.finalState) {
    protocolFailure(
      context,
      websocket,
      'The server sent control data after the session ended.',
    );
    return;
  }

  switch (message.type) {
    case 'ready':
      if (context.ready || context.finalState) {
        protocolFailure(
          context,
          websocket,
          'The server sent an unexpected ready message.',
        );
        return;
      }
      context.ready = true;
      clearHandshakeTimer();
      reconnectAttempt = 0;
      setConnectionState('ready');
      sendResize();
      if (
        desktopPointer.matches &&
        !activeSheet &&
        (!hasFocusedDesktopTerminal || restoreDesktopTerminalFocus)
      ) {
        hasFocusedDesktopTerminal = true;
        restoreDesktopTerminalFocus = false;
        terminal.focus();
        syncTypeButton();
      }
      break;
    case 'agent-done':
      if (
        !context.ready ||
        (message.agent !== undefined && typeof message.agent !== 'string') ||
        (message.title !== undefined && typeof message.title !== 'string')
      ) {
        protocolFailure(
          context,
          websocket,
          'The server sent an invalid completion message.',
        );
        return;
      }
      showAgentCompletion(message);
      break;
    case 'exit':
      if (
        !context.ready ||
        context.finalState ||
        !Number.isInteger(message.code)
      ) {
        protocolFailure(
          context,
          websocket,
          'The server sent an invalid exit message.',
        );
        return;
      }
      context.finalState = { state: 'ended', code: message.code };
      setConnectionState('ended', { code: message.code });
      break;
    case 'error': {
      if (
        typeof message.message !== 'string' ||
        message.message.trim().length === 0
      ) {
        protocolFailure(
          context,
          websocket,
          'The server sent an invalid error message.',
        );
        return;
      }
      const serverMessage = message.message.trim();
      if (serverMessage.toLowerCase() === ACTIVE_ATTACHMENT_MESSAGE) {
        if (context.automatic) {
          setConnectionState('reconnecting', { seconds: 1 });
        } else {
          context.finalState = { state: 'limited' };
          setConnectionState('limited');
        }
      } else {
        const detail = conciseMessage(serverMessage);
        context.finalState = { state: 'error', detail };
        setConnectionState('error', { detail });
      }
      break;
    }
    default:
      protocolFailure(
        context,
        websocket,
        'The server sent an unknown control message.',
      );
  }
}

function writeServerOutput(context, websocket, data) {
  if (!context.ready || context.finalState) {
    protocolFailure(
      context,
      websocket,
      'The server sent terminal output at an invalid time.',
    );
    return;
  }

  const payload = new Uint8Array(data);
  context.pendingOutputBytes += payload.byteLength;
  if (context.pendingOutputBytes > MAX_PENDING_OUTPUT_BYTES) {
    const detail = 'Terminal output exceeded the browser buffer.';
    context.finalState = { state: 'error', detail };
    setConnectionState('error', { detail });
    websocket.close(1009, 'browser output buffer full');
    return;
  }
  try {
    terminal.write(payload, () => {
      context.pendingOutputBytes = Math.max(
        0,
        context.pendingOutputBytes - payload.byteLength,
      );
    });
  } catch {
    protocolFailure(
      context,
      websocket,
      'The terminal could not render server output.',
    );
  }
}

function openAttachment(session, generation, automatic) {
  let websocket;
  try {
    websocket = new WebSocket(attachURL(), ATTACH_PROTOCOL);
  } catch (error) {
    setConnectionState('error', { detail: conciseMessage(error?.message) });
    return;
  }

  const context = {
    ready: false,
    finalState: null,
    automatic,
    pendingOutputBytes: 0,
  };

  socket = websocket;
  websocket.binaryType = 'arraybuffer';
  handshakeTimer = window.setTimeout(() => {
    if (
      socket === websocket &&
      generation === connectionGeneration &&
      !context.finalState
    ) {
      failAndReconnect();
    }
  }, HANDSHAKE_TIMEOUT_MS);

  websocket.addEventListener('open', () => {
    if (
      socket !== websocket ||
      generation !== connectionGeneration ||
      pageSuspended
    ) {
      websocket.close(1000, 'obsolete connection');
      return;
    }
    if (websocket.protocol !== ATTACH_PROTOCOL) {
      protocolFailure(
        context,
        websocket,
        'The server did not accept the Herdr protocol.',
      );
      return;
    }

    fitTerminal();
    const dimensions = currentDimensions();
    lastSentDimensions = dimensions;
    setConnectionState('connecting', { detail: 'Starting the terminal…' });

    try {
      websocket.send(
        JSON.stringify({
          type: 'hello',
          nonce: session.nonce,
          ...dimensions,
        }),
      );
      session.nonce = '';
    } catch {
      failAndReconnect();
    }
  });

  websocket.addEventListener('message', (event) => {
    if (socket !== websocket || generation !== connectionGeneration) {
      return;
    }
    if (typeof event.data === 'string') {
      handleServerControl(context, websocket, event.data);
      return;
    }
    if (event.data instanceof ArrayBuffer) {
      writeServerOutput(context, websocket, event.data);
      return;
    }
    if (event.data instanceof Blob) {
      void event.data
        .arrayBuffer()
        .then((buffer) => {
          if (socket === websocket && generation === connectionGeneration) {
            writeServerOutput(context, websocket, buffer);
          }
        })
        .catch(() => {
          if (socket === websocket && generation === connectionGeneration) {
            protocolFailure(
              context,
              websocket,
              'The server sent invalid terminal output.',
            );
          }
        });
      return;
    }
    protocolFailure(
      context,
      websocket,
      'The server sent an unsupported frame.',
    );
  });

  websocket.addEventListener('close', () => {
    if (generation !== connectionGeneration) {
      return;
    }
    if (socket === websocket) {
      socket = null;
    }
    clearHandshakeTimer();

    if (pageSuspended) {
      return;
    }
    if (context.finalState) {
      setConnectionState(context.finalState.state, context.finalState);
      return;
    }
    scheduleReconnect();
  });

  websocket.addEventListener('error', () => {
    // Browsers intentionally hide WebSocket handshake details; close handles retry state.
  });
}

async function beginConnection({
  automatic = false,
  resetBackoff = false,
} = {}) {
  clearReconnectTimers();
  if (resetBackoff) {
    reconnectAttempt = 0;
  }
  if (pageSuspended) {
    return;
  }
  if (!navigator.onLine) {
    setConnectionState('offline');
    return;
  }

  stopCurrentTransport('new connection');
  const generation = connectionGeneration;
  sessionController = new AbortController();
  setConnectionState('connecting', {
    detail: automatic
      ? 'Refreshing the terminal session…'
      : 'Starting the terminal session…',
  });

  try {
    const session = await requestSession(sessionController.signal);
    if (generation !== connectionGeneration || pageSuspended) {
      return;
    }
    sessionController = null;
    openAttachment(session, generation, automatic);
  } catch (error) {
    if (
      generation !== connectionGeneration ||
      (error instanceof DOMException && error.name === 'AbortError')
    ) {
      return;
    }
    sessionController = null;
    if (error instanceof AttachmentLimitError && !automatic) {
      setConnectionState('limited');
    } else if (automatic) {
      scheduleReconnect();
    } else {
      setConnectionState('error', { detail: conciseMessage(error?.message) });
    }
  }
}

async function pasteFromClipboard() {
  if (pastePending || !isTransportReady()) {
    return;
  }

  pastePending = true;
  syncConnectionControls();
  try {
    if (
      !window.isSecureContext ||
      typeof navigator.clipboard?.readText !== 'function'
    ) {
      throw new Error('Clipboard reading is unavailable.');
    }
    const text = await navigator.clipboard.readText();
    if (!text) {
      announce('Clipboard is empty.');
      return;
    }
    if (!isTransportReady()) {
      announce('Paste was not sent because Herdr disconnected.');
      return;
    }
    if (pasteIntoTerminal(text)) {
      announce('Clipboard text inserted.');
    }
  } catch {
    announce(
      'Clipboard access was blocked. Tap Type, then use the keyboard Paste command.',
    );
  } finally {
    pastePending = false;
    syncConnectionControls();
  }
}

connectionAction.addEventListener('click', () => {
  void beginConnection({ resetBackoff: true });
});
terminalElement.addEventListener('pointerdown', beginTerminalPointer, {
  passive: true,
});
terminalElement.addEventListener('pointermove', moveTerminalPointer, {
  passive: false,
});
terminalElement.addEventListener('pointerup', endTerminalPointer, {
  passive: true,
});
terminalElement.addEventListener('pointercancel', endTerminalPointer, {
  passive: true,
});

terminalElement.addEventListener('focusin', () => {
  if (!desktopPointer.matches && connectionState === 'ready') {
    mobileTerminalValue = terminalTextarea?.value || '';
    mobileTerminalIsComposing = false;
    mobileKeyboardEnabled = true;
  }
  syncTypeButton();
});
terminalElement.addEventListener('focusout', () => {
  window.setTimeout(() => {
    if (!desktopPointer.matches && !terminalHasFocus()) {
      mobileKeyboardEnabled = false;
    }
    syncTypeButton();
  }, 0);
});

desktopPointer.addEventListener('change', () => {
  mobileKeyboardEnabled = false;
  syncMobileSwitcherMask();
  syncTypeButton();
});

document.addEventListener('focusin', (event) => {
  if (
    connectionState !== 'ready' &&
    event.target instanceof Element &&
    !terminalElement.contains(event.target)
  ) {
    restoreDesktopTerminalFocus = false;
  }
});

document.addEventListener('click', (event) => {
  const target =
    event.target instanceof Element ? event.target.closest('button') : null;
  if (!target || target.disabled) {
    return;
  }

  if (target.hasAttribute('data-close-sheet')) {
    const sheet = target.closest('dialog');
    if (sheet) {
      closeSheet(sheet);
    }
    return;
  }

  const action = target.dataset.action;
  switch (action) {
    case 'toggle-keyboard':
      if (desktopPointer.matches ? terminalHasFocus() : mobileKeyboardEnabled) {
        closeTerminalKeyboard();
      } else if (isTransportReady()) {
        mobileTerminalValue = terminalTextarea?.value || '';
        mobileTerminalIsComposing = false;
        mobileKeyboardEnabled = !desktopPointer.matches;
        if (terminalTextarea) {
          terminalTextarea.focus({ preventScroll: true });
        } else {
          terminal.focus();
        }
        syncTypeButton();
      }
      break;
    case 'toggle-switcher':
      if (!desktopPointer.matches) {
        closeTerminalKeyboard();
      }
      if (sendText(mobileSwitcherMouseSequence(terminal.cols))) {
        announce('Herdr switcher toggled.');
      }
      break;
    case 'open-keys':
      openSheet(
        keysSheet,
        target,
        keysSheet.querySelector('[data-terminal-key]'),
      );
      break;
    case 'open-herdr':
      openSheet(
        herdrSheet,
        target,
        herdrSheet.querySelector('[data-herdr-key]'),
      );
      break;
    case 'toggle-screen-reader':
      toggleScreenReaderMode();
      break;
    case 'paste':
      void pasteFromClipboard();
      break;
    default:
      break;
  }

  const terminalKey = target.dataset.terminalKey;
  if (terminalKey && TERMINAL_KEYS.has(terminalKey)) {
    if (sendBytes(bytesForTerminalKey(terminalKey))) {
      announce(`${TERMINAL_KEY_NAMES.get(terminalKey)} sent.`);
    }
  }

  const herdrKey = target.dataset.herdrKey;
  if (typeof herdrKey === 'string' && herdrKey.length === 1) {
    const code = herdrKey.charCodeAt(0);
    if (code <= 0x7f && sendBytes(Uint8Array.of(0x00, code))) {
      const actionName =
        target.firstElementChild?.textContent || 'Herdr shortcut';
      closeSheet(herdrSheet);
      announce(`${actionName} sent.`);
    }
  }
});

window.addEventListener('offline', () => {
  clearReconnectTimers();
  stopCurrentTransport('browser offline');
  setConnectionState('offline');
});

window.addEventListener('online', () => {
  if (!pageSuspended && connectionState !== 'ready') {
    void beginConnection({ automatic: true, resetBackoff: true });
  }
});

window.addEventListener('pagehide', () => {
  pageSuspended = true;
  clearReconnectTimers();
  if (fitTimer !== null) {
    window.clearTimeout(fitTimer);
    fitTimer = null;
  }
  stopCurrentTransport('page hidden');
});

window.addEventListener('pageshow', (event) => {
  if (pageSuspended || event.persisted) {
    pageSuspended = false;
    syncVisualViewport();
    scheduleFit(true);
    void beginConnection({ automatic: true, resetBackoff: true });
  }
});

syncVisualViewport();
scheduleFit(true);
syncScreenReaderButton();
setConnectionState('connecting');
void beginConnection();
