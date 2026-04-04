function sendWSControlCommand(
  payload,
  {
    waitFor = [],
    laneHint = '',
    buildWSURLCandidates,
    nextWSRequestID,
    WebSocketImpl = WebSocket,
  } = {},
) {
  const sid = String(payload?.session_id || '').trim();
  if (!sid) throw new Error('session_id is required');
  const requestID = String(payload?.request_id || nextWSRequestID(String(payload?.type || 'ws'))).trim();
  const candidates = buildWSURLCandidates('/api/coding/ws');
  if (candidates.length === 0) {
    throw new Error('WebSocket endpoint unavailable.');
  }
  const expected = new Set((Array.isArray(waitFor) ? waitFor : []).map((item) => String(item || '').trim().toLowerCase()).filter(Boolean));
  return new Promise((resolve, reject) => {
    let attemptIndex = 0;
    let settled = false;
    let ws = null;
    const finish = (fn, value) => {
      if (settled) return;
      settled = true;
      try {
        if (ws && (ws.readyState === WebSocketImpl.OPEN || ws.readyState === WebSocketImpl.CONNECTING)) ws.close();
      } catch {
      }
      fn(value);
    };
    const connect = () => {
      if (settled) return;
      if (attemptIndex >= candidates.length) {
        finish(reject, new Error('WebSocket control command failed.'));
        return;
      }
      ws = new WebSocketImpl(candidates[attemptIndex]);
      attemptIndex += 1;
      let didOpen = false;
      ws.onopen = () => {
        didOpen = true;
        try {
          ws.send(JSON.stringify({ ...payload, request_id: requestID }));
        } catch (error) {
          finish(reject, error instanceof Error ? error : new Error('Failed to send control command.'));
        }
      };
      ws.onmessage = (event) => {
        let evt = {};
        try {
          evt = JSON.parse(String(event?.data || '{}'));
        } catch {
          return;
        }
        const eventType = String(evt?.event || evt?.event_type || '').trim().toLowerCase();
        const evtRequestID = String(evt?.request_id || evt?.payload?.request_id || '').trim();
        const evtLane = String(evt?.lane || evt?.payload?.lane || '').trim().toLowerCase();
        if (evtRequestID && evtRequestID !== requestID) return;
        if (laneHint && evtLane && evtLane !== laneHint) return;
        if (eventType === 'session.error') {
          const msg = String(evt?.message || evt?.payload?.message || 'Command failed.');
          finish(reject, new Error(msg));
          return;
        }
        if (eventType === 'session.duplicate_request') {
          finish(resolve, evt);
          return;
        }
        if (expected.size === 0 || expected.has(eventType)) {
          finish(resolve, evt);
        }
      };
      ws.onerror = () => {
        try {
          ws.close();
        } catch {
        }
      };
      ws.onclose = () => {
        if (settled) return;
        if (!didOpen) {
          connect();
          return;
        }
        finish(reject, new Error('WebSocket control command closed before completion.'));
      };
    };
    connect();
  });
}

function streamChatViaWebSocket(
  payload,
  onEvent,
  {
    buildWSURLCandidates,
    nextWSRequestID,
    setActiveStreamSocket = () => {},
    clearActiveStreamSocket = () => {},
    WebSocketImpl = WebSocket,
  } = {},
) {
  return new Promise((resolve, reject) => {
    const candidates = buildWSURLCandidates('/api/coding/ws');
    if (candidates.length === 0) {
      reject(new Error('WebSocket endpoint unavailable.'));
      return;
    }

    let settled = false;
    let attemptIndex = 0;
    let ws = null;
    let startedAck = false;
    let runtimeStatusDoneTimer = null;
    const cancelRuntimeStatusDoneTimer = () => {
      if (!runtimeStatusDoneTimer) return;
      clearTimeout(runtimeStatusDoneTimer);
      runtimeStatusDoneTimer = null;
    };
    const requestID = nextWSRequestID('send');
    const expectedSessionID = String(payload?.session_id || '').trim();
    const finish = (fn, value) => {
      if (settled) return;
      settled = true;
      cancelRuntimeStatusDoneTimer();
      clearActiveStreamSocket(ws);
      try {
        if (ws && (ws.readyState === WebSocketImpl.OPEN || ws.readyState === WebSocketImpl.CONNECTING)) {
          ws.close();
        }
      } catch {
      }
      fn(value);
    };

    const connectAttempt = () => {
      if (settled) return;
      if (attemptIndex >= candidates.length) {
        finish(reject, new Error('WebSocket stream error.'));
        return;
      }
      const wsURL = candidates[attemptIndex];
      attemptIndex += 1;
      ws = new WebSocketImpl(wsURL);
      setActiveStreamSocket(ws);
      let didOpen = false;

      ws.onopen = () => {
        didOpen = true;
        try {
          ws.send(JSON.stringify({
            type: 'session.send',
            request_id: requestID,
            session_id: expectedSessionID,
            content: payload.content,
            model: payload.model,
            reasoning_level: payload.reasoning_level,
            work_dir: payload.work_dir,
            sandbox_mode: payload.sandbox_mode,
            command: 'chat',
            last_seen_event_seq: Number(payload?.last_seen_event_seq || 0),
          }));
        } catch (err) {
          finish(reject, err instanceof Error ? err : new Error('Failed to send websocket payload.'));
        }
      };

      ws.onmessage = (event) => {
        let evt = {};
        try {
          evt = JSON.parse(String(event?.data || '{}'));
        } catch {
          return;
        }
        const eventType = String(evt?.event || evt?.event_type || '').trim().toLowerCase();
        if (runtimeStatusDoneTimer) {
          if (eventType === 'session.stream' || eventType === 'session.snapshot' || eventType === 'session.started') {
            cancelRuntimeStatusDoneTimer();
          }
        }
        if (String(evt?.session_id || '').trim() && String(evt?.session_id || '').trim() !== expectedSessionID) {
          return;
        }
        if (onEvent) onEvent(evt);
        if (eventType === 'session.started') {
          startedAck = true;
          return;
        }
        if (eventType === 'session.done') {
          cancelRuntimeStatusDoneTimer();
          finish(resolve, evt);
          return;
        }
        if (eventType === 'session.error') {
          finish(reject, new Error(String(evt?.message || evt?.payload?.message || 'Streaming failed.')));
        }
      };

      ws.onerror = () => {
        try {
          ws.close();
        } catch {
        }
      };

      ws.onclose = () => {
        if (settled) return;
        if (!didOpen) {
          connectAttempt();
          return;
        }
        if (startedAck) {
          finish(reject, new Error('websocket_detached_background'));
          return;
        }
        finish(reject, new Error('WebSocket connection failed before run start.'));
      };
    };

    connectAttempt();
  });
}

export {
  sendWSControlCommand,
  streamChatViaWebSocket,
};
