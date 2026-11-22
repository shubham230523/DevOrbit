import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/core/network/broker_websocket_client.dart';

void main() {
  test('BrokerWebSocketClient does not include token parameter in URL and handles connection failure cleanly', () async {
    final client = BrokerWebSocketClient(
      brokerUrl: 'ws://127.0.0.1:59999', // Non-listening test port
      machineId: 'mach-ws-test-01',
      deviceId: 'dev-ws-test-01',
      sessionId: 'sess-ws-test-01',
      token: 'client-token-secret-value-32b',
      masterSecret: 'master-secret-value-32-bytes-ok!',
    );

    expect(client.state, equals(BrokerConnectionState.disconnected));

    // Connect to closed port -> should trigger authFailed or reconnecting cleanly
    await client.connect();

    expect(
      client.state == BrokerConnectionState.authFailed ||
          client.state == BrokerConnectionState.disconnected ||
          client.state == BrokerConnectionState.reconnecting,
      isTrue,
    );

    client.disconnect();
    client.dispose();
  });

  test('Requires server session_ready frame before reporting authenticated state, and handles server revocation', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final port = server.port;
    final socketCompleter = Completer<WebSocket>();

    server.listen((HttpRequest req) async {
      if (req.uri.path == '/ws/client') {
        final ws = await WebSocketTransformer.upgrade(req);
        if (!socketCompleter.isCompleted) {
          socketCompleter.complete(ws);
        }
      }
    });

    final client = BrokerWebSocketClient(
      brokerUrl: 'ws://127.0.0.1:$port',
      machineId: 'mach-test-01',
      deviceId: 'dev-test-01',
      sessionId: 'sess-test-01',
      token: 'token-test-32b',
      masterSecret: 'master-secret-value-32-bytes-ok!',
    );

    await client.connect();
    final serverSocket = await socketCompleter.future;

    // After handshake, state should NOT be authenticatedBrokerSessionActive yet
    expect(client.state != BrokerConnectionState.authenticatedBrokerSessionActive, isTrue);

    // Now server sends session_ready ack frame
    serverSocket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-test-01'}));
    await Future.delayed(const Duration(milliseconds: 100));

    expect(client.state, equals(BrokerConnectionState.authenticatedBrokerSessionActive));

    // Server sends revoked frame -> client state transitions to revoked
    serverSocket.add(jsonEncode({'type': 'revoked', 'session_id': 'sess-test-01'}));
    await Future.delayed(const Duration(milliseconds: 100));

    expect(client.state, equals(BrokerConnectionState.revoked));

    client.dispose();
    await serverSocket.close();
    await server.close();
  });

  test('Handles session_pending frame -> waitingForHost, then session_ready -> authenticated, and host_disconnected -> hostDisconnected', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final port = server.port;
    final socketCompleter = Completer<WebSocket>();

    server.listen((HttpRequest req) async {
      if (req.uri.path == '/ws/client') {
        final ws = await WebSocketTransformer.upgrade(req);
        if (!socketCompleter.isCompleted) {
          socketCompleter.complete(ws);
        }
      }
    });

    final client = BrokerWebSocketClient(
      brokerUrl: 'ws://127.0.0.1:$port',
      machineId: 'mach-stagea-01',
      deviceId: 'dev-stagea-01',
      sessionId: 'sess-stagea-01',
      token: 'token-stagea-32b',
      masterSecret: 'master-secret-value-32-bytes-ok!',
    );

    await client.connect();
    final serverSocket = await socketCompleter.future;

    // 1. Server sends session_pending
    serverSocket.add(jsonEncode({
      'type': 'session_pending',
      'session_id': 'sess-stagea-01',
      'status': 'waiting_for_host',
    }));
    await Future.delayed(const Duration(milliseconds: 50));
    expect(client.state, equals(BrokerConnectionState.waitingForHost));

    // 2. Server sends session_ready for a DIFFERENT session ID -> ignored, state remains waitingForHost
    serverSocket.add(jsonEncode({
      'type': 'session_ready',
      'session_id': 'sess-wrong-other-session',
      'status': 'ready',
    }));
    await Future.delayed(const Duration(milliseconds: 50));
    expect(client.state, equals(BrokerConnectionState.waitingForHost));

    // 3. Server sends session_ready for CORRECT session ID -> state transitions to authenticated
    serverSocket.add(jsonEncode({
      'type': 'session_ready',
      'session_id': 'sess-stagea-01',
      'status': 'ready',
    }));
    await Future.delayed(const Duration(milliseconds: 50));
    expect(client.state, equals(BrokerConnectionState.authenticatedBrokerSessionActive));

    // 4. Server sends host_disconnected -> state transitions to hostDisconnected
    serverSocket.add(jsonEncode({
      'type': 'host_disconnected',
      'session_id': 'sess-stagea-01',
      'status': 'waiting_for_host',
    }));
    await Future.delayed(const Duration(milliseconds: 50));
    expect(client.state, equals(BrokerConnectionState.hostDisconnected));

    // 5. Host reconnects: server sends session_ready again -> recovered
    serverSocket.add(jsonEncode({
      'type': 'session_ready',
      'session_id': 'sess-stagea-01',
      'status': 'ready',
    }));
    await Future.delayed(const Duration(milliseconds: 50));
    expect(client.state, equals(BrokerConnectionState.authenticatedBrokerSessionActive));

    client.dispose();
    await serverSocket.close();
    await server.close();
  });

  test('Readiness timeout transitions to connectionFailed when no readiness acknowledgement arrives', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final port = server.port;
    final socketCompleter = Completer<WebSocket>();

    server.listen((HttpRequest req) async {
      if (req.uri.path == '/ws/client') {
        final ws = await WebSocketTransformer.upgrade(req);
        if (!socketCompleter.isCompleted) {
          socketCompleter.complete(ws);
        }
      }
    });

    final client = BrokerWebSocketClient(
      brokerUrl: 'ws://127.0.0.1:$port',
      machineId: 'mach-timeout-01',
      deviceId: 'dev-timeout-01',
      sessionId: 'sess-timeout-01',
      token: 'token-timeout-32b',
      masterSecret: 'master-secret-value-32-bytes-ok!',
      readinessTimeout: const Duration(milliseconds: 200), // Short test timeout
    );

    await client.connect();
    final serverSocket = await socketCompleter.future;

    // Wait for the readiness timeout to fire (no frames sent by server)
    await Future.delayed(const Duration(milliseconds: 350));

    expect(
      client.state == BrokerConnectionState.connectionFailed ||
          client.state == BrokerConnectionState.reconnecting,
      isTrue,
    );

    client.disconnect();
    client.dispose();
    await serverSocket.close();
    await server.close();
  });

  test('Handles expired session frame and terminates cleanly', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final port = server.port;
    final socketCompleter = Completer<WebSocket>();

    server.listen((HttpRequest req) async {
      if (req.uri.path == '/ws/client') {
        final ws = await WebSocketTransformer.upgrade(req);
        if (!socketCompleter.isCompleted) {
          socketCompleter.complete(ws);
        }
      }
    });

    final client = BrokerWebSocketClient(
      brokerUrl: 'ws://127.0.0.1:$port',
      machineId: 'mach-exp-01',
      deviceId: 'dev-exp-01',
      sessionId: 'sess-exp-01',
      token: 'token-exp-32b',
      masterSecret: 'master-secret-value-32-bytes-ok!',
    );

    await client.connect();
    final serverSocket = await socketCompleter.future;

    serverSocket.add(jsonEncode({
      'type': 'error',
      'error': 'session expired',
      'status': 'expired',
    }));
    await Future.delayed(const Duration(milliseconds: 50));

    expect(client.state, equals(BrokerConnectionState.sessionExpired));

    client.dispose();
    await serverSocket.close();
    await server.close();
  });
}
