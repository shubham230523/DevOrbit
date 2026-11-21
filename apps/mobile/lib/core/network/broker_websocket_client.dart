import 'dart:async';
import 'package:web_socket_channel/web_socket_channel.dart';

enum BrokerConnectionState {
  disconnected,
  connecting,
  authenticatedBrokerSessionActive,
  reconnecting,
  authFailed,
  revoked,
}

class BrokerWebSocketClient {
  final String brokerUrl;
  final String machineId;
  final String deviceId;
  final String sessionId;
  final String token;

  WebSocketChannel? _channel;
  BrokerConnectionState _state = BrokerConnectionState.disconnected;
  final StreamController<BrokerConnectionState> _stateController = StreamController<BrokerConnectionState>.broadcast();

  BrokerConnectionState get state => _state;
  Stream<BrokerConnectionState> get stateStream => _stateController.stream;

  BrokerWebSocketClient({
    required this.brokerUrl,
    required this.machineId,
    required this.deviceId,
    required this.sessionId,
    required this.token,
  });

  Future<void> connect() async {
    _state = BrokerConnectionState.connecting;
    _stateController.add(_state);

    try {
      final wsUri = Uri.parse(
        '$brokerUrl/ws/client?machine_id=$machineId&device_id=$deviceId&session_id=$sessionId&token=$token',
      );

      _channel = WebSocketChannel.connect(wsUri);
      await _channel!.ready;

      _state = BrokerConnectionState.authenticatedBrokerSessionActive;
      _stateController.add(_state);

      _channel!.stream.listen(
        (data) {
          // Process incoming encrypted frame payload from broker
        },
        onError: (error) {
          _state = BrokerConnectionState.reconnecting;
          _stateController.add(_state);
        },
        onDone: () {
          _state = BrokerConnectionState.disconnected;
          _stateController.add(_state);
        },
      );
    } catch (e) {
      _state = BrokerConnectionState.authFailed;
      _stateController.add(_state);
    }
  }

  void disconnect() {
    _channel?.sink.close();
    _state = BrokerConnectionState.disconnected;
    _stateController.add(_state);
  }

  void dispose() {
    _channel?.sink.close();
    _stateController.close();
  }
}
