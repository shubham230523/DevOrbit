import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

enum BrokerConnectionState {
  disconnected,
  connecting,
  authenticating,
  authenticatedBrokerSessionActive,
  reconnecting,
  authFailed,
  revoked,
}

class ApplicationResponseEnvelope {
  final String protocolVersion;
  final String sessionId;
  final String requestId;
  final int statusCode;
  final Map<String, dynamic>? headers;
  final String body;

  ApplicationResponseEnvelope({
    required this.protocolVersion,
    required this.sessionId,
    required this.requestId,
    required this.statusCode,
    this.headers,
    required this.body,
  });

  factory ApplicationResponseEnvelope.fromJson(Map<String, dynamic> json) {
    return ApplicationResponseEnvelope(
      protocolVersion: json['v'] ?? '1.2',
      sessionId: json['session_id'] ?? '',
      requestId: json['request_id'] ?? '',
      statusCode: json['status_code'] ?? 200,
      headers: json['headers'] as Map<String, dynamic>?,
      body: json['body'] ?? '',
    );
  }
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
  final Map<String, Completer<ApplicationResponseEnvelope>> _pendingRequests = {};
  int _sequenceNum = 0;

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

      _state = BrokerConnectionState.authenticating;
      _stateController.add(_state);

      _channel!.stream.listen(
        (data) {
          try {
            final jsonMap = jsonDecode(data.toString()) as Map<String, dynamic>;
            final frameType = jsonMap['type'];

            if (frameType == 'data' && jsonMap['encrypted_payload'] != null) {
              final payloadStr = jsonMap['encrypted_payload'].toString();
              if (payloadStr.startsWith('{')) {
                final appRespJson = jsonDecode(payloadStr) as Map<String, dynamic>;
                final appResp = ApplicationResponseEnvelope.fromJson(appRespJson);

                if (_pendingRequests.containsKey(appResp.requestId)) {
                  _pendingRequests.remove(appResp.requestId)!.complete(appResp);
                }
              }
            }
          } catch (_) {}
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

      _state = BrokerConnectionState.authenticatedBrokerSessionActive;
      _stateController.add(_state);
    } catch (e) {
      _state = BrokerConnectionState.authFailed;
      _stateController.add(_state);
    }
  }

  Future<ApplicationResponseEnvelope> sendRequest({
    required String method,
    required String path,
    Map<String, String>? headers,
    String? body,
    Duration timeout = const Duration(seconds: 8),
  }) async {
    if (_state != BrokerConnectionState.authenticatedBrokerSessionActive || _channel == null) {
      throw StateError('Broker WebSocket is not connected or authenticated');
    }

    _sequenceNum++;
    final requestId = 'req_${_sequenceNum}_${DateTime.now().millisecondsSinceEpoch}';

    final appReq = {
      'v': '1.2',
      'session_id': sessionId,
      'request_id': requestId,
      'method': method,
      'path': path,
      if (headers != null) 'headers': headers,
      'body': body ?? '',
      'seq_num': _sequenceNum,
      'timestamp': DateTime.now().microsecondsSinceEpoch,
    };

    final plainJson = jsonEncode(appReq);

    final frameEnvelope = {
      'v': '1.2',
      'session_id': sessionId,
      'type': 'data',
      'encrypted_payload': plainJson,
      'seq_num': _sequenceNum,
      'timestamp': DateTime.now().microsecondsSinceEpoch,
    };

    final completer = Completer<ApplicationResponseEnvelope>();
    _pendingRequests[requestId] = completer;

    _channel!.sink.add(jsonEncode(frameEnvelope));

    return completer.future.timeout(
      timeout,
      onTimeout: () {
        _pendingRequests.remove(requestId);
        throw TimeoutException('Request $requestId timed out waiting for response frame');
      },
    );
  }

  void disconnect() {
    _channel?.sink.close();
    _state = BrokerConnectionState.disconnected;
    _stateController.add(_state);
  }

  void dispose() {
    _channel?.sink.close();
    _stateController.close();
    for (final completer in _pendingRequests.values) {
      if (!completer.isCompleted) {
        completer.completeError(StateError('Client disposed'));
      }
    }
    _pendingRequests.clear();
  }
}
