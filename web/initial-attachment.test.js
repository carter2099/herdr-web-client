import { describe, expect, test } from 'bun:test';
import { startInitialAttachment } from './initial-attachment.js';

function fakeSocket() {
  const socket = {
    readyState: WebSocket.CONNECTING,
    closed: null,
    close(code, reason) {
      socket.closed = { code, reason };
      socket.readyState = WebSocket.CLOSED;
    },
  };
  return socket;
}

describe('initial attachment', () => {
  test('opens the WebSocket as soon as the session request succeeds', async () => {
    const socket = fakeSocket();
    const attachment = startInitialAttachment({
      requestSession: async () => new Response('{}', { status: 200 }),
      openSocket: () => socket,
    });

    expect(await attachment.socket).toBe(socket);
    expect((await attachment.response).status).toBe(200);
  });

  test.each([
    ['an attachment conflict', 409],
    ['a forbidden request', 403],
    ['an opaque access redirect', 0],
  ])('never opens a WebSocket after %s', async (_name, status) => {
    let opened = false;
    const attachment = startInitialAttachment({
      requestSession: async () =>
        status === 0
          ? Response.error()
          : new Response('{}', { status: status }),
      openSocket: () => {
        opened = true;
        return fakeSocket();
      },
    });

    expect(await attachment.socket).toBeNull();
    expect(opened).toBe(false);
  });

  test('never opens a WebSocket after a failed request', async () => {
    let opened = false;
    const attachment = startInitialAttachment({
      requestSession: async () => {
        throw new TypeError('network failure');
      },
      openSocket: () => {
        opened = true;
        return fakeSocket();
      },
    });

    expect(await attachment.socket).toBeNull();
    expect(opened).toBe(false);
    await expect(attachment.response).rejects.toThrow('network failure');
  });

  test('discarding aborts the request and closes a socket opened afterwards', async () => {
    let finishRequest;
    let requestSignal;
    const socket = fakeSocket();
    const attachment = startInitialAttachment({
      requestSession: (signal) => {
        requestSignal = signal;
        return new Promise((resolve) => {
          finishRequest = resolve;
        });
      },
      openSocket: () => socket,
    });

    attachment.discard();
    expect(requestSignal.aborted).toBe(true);

    finishRequest(new Response('{}', { status: 200 }));
    await attachment.socket;
    await Promise.resolve();
    expect(socket.closed).toEqual({
      code: 1000,
      reason: 'obsolete connection',
    });
  });
});
