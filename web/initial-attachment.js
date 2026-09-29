// The first page load requests its session and opens the WebSocket while the
// terminal font loads and xterm initializes. The server consumes the nonce only
// from the hello message, which the app still sends after fitting the terminal,
// so an early socket never starts a terminal on its own.
export function startInitialAttachment({ requestSession, openSocket }) {
  const controller = new AbortController();
  const response = requestSession(controller.signal);
  // The server rejects an upgrade while another socket is attached, so a
  // conflicting or failed session request must never be followed by a socket.
  const socket = response
    .then((value) => (value.status === 200 ? openSocket() : null))
    .catch(() => null);
  // Callers that discard the attachment never await the request itself.
  response.catch(() => {});
  return {
    controller,
    response,
    socket,
    discard() {
      controller.abort();
      void socket.then(closeUnusedSocket);
    },
  };
}

export function closeUnusedSocket(websocket) {
  if (!websocket || websocket.readyState >= WebSocket.CLOSING) {
    return;
  }
  try {
    websocket.close(1000, 'obsolete connection');
  } catch {
    // The socket has no listeners; closing is only resource cleanup.
  }
}
