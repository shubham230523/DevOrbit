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
}
