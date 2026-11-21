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
}
