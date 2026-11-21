import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/io.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import '../security/e2e_session.dart';

enum BrokerConnectionState {
  disconnected,
  connecting,
  transportConnected,
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
  final String masterSecret;

  WebSocketChannel? _channel;
  FlutterE2ESession? _e2eSession;
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
    required this.masterSecret,
  });

  Future<void> connect() async {
    _state = BrokerConnectionState.connecting;
    _stateController.add(_state);

    try {
      _e2eSession = await FlutterE2ESession.create(
        sessionId: sessionId,
        deviceId: deviceId,
        masterSecret: masterSecret,
      );

      final baseUri = Uri.parse(brokerUrl);
      if (baseUri.scheme.toLowerCase() != 'ws' && baseUri.scheme.toLowerCase() != 'wss') {
        throw ArgumentError('brokerUrl must use ws:// or wss://, got: ${baseUri.scheme}');
      }

      final wsUri = Uri.parse(
        '$brokerUrl/ws/client?machine_id=$machineId&device_id=$deviceId&session_id=$sessionId',
      );

      _channel = IOWebSocketChannel.connect(
        wsUri,
        headers: {
          'Authorization': 'Bearer $token',
        },
      );
      await _channel!.ready;

      _state = BrokerConnectionState.transportConnected;
      _stateController.add(_state);

      _state = BrokerConnectionState.authenticating;
      _stateController.add(_state);

      _channel!.stream.listen(
        (data) async {
          try {
            final jsonMap = jsonDecode(data.toString()) as Map<String, dynamic>;
            final frameType = jsonMap['type'];

            // 1. Server-confirmed session acknowledgment
            if (frameType == 'session_ready' || frameType == 'auth_ack') {
              _state = BrokerConnectionState.authenticatedBrokerSessionActive;
              _stateController.add(_state);
              return;
            }

            // 2. Server-side session revocation
            if (frameType == 'revoked') {
              _state = BrokerConnectionState.revoked;
              _stateController.add(_state);
              _failPendingRequests(Exception('Session revoked by server'));
              _channel?.sink.close();
              _channel = null;
              return;
            }

            final cipherHex = jsonMap['encrypted_payload']?.toString();
            final seqNum = jsonMap['seq_num'] as int? ?? 1;
            final timestamp = jsonMap['timestamp'] as int? ?? DateTime.now().microsecondsSinceEpoch;

            if (frameType == 'data' && cipherHex != null && _e2eSession != null) {
              try {
                final decryptedJsonStr = await _e2eSession!.decrypt(
                  direction: Direction.hostToClient,
                  cipherTextHex: cipherHex,
                  seqNum: seqNum,
                  timestamp: timestamp,
                );

                final appRespJson = jsonDecode(decryptedJsonStr) as Map<String, dynamic>;
                final appResp = ApplicationResponseEnvelope.fromJson(appRespJson);

                if (_pendingRequests.containsKey(appResp.requestId)) {
                  _pendingRequests.remove(appResp.requestId)!.complete(appResp);
                }
              } catch (_) {}
            }
          } catch (_) {}
        },
        onError: (error) {
          if (_state != BrokerConnectionState.revoked) {
            _state = BrokerConnectionState.reconnecting;
            _stateController.add(_state);
          }
          _failPendingRequests(Exception('WebSocket error: $error'));
        },
        onDone: () {
          if (_state != BrokerConnectionState.revoked) {
            _state = BrokerConnectionState.disconnected;
            _stateController.add(_state);
          }
          _failPendingRequests(Exception('WebSocket connection closed'));
        },
      );
    } catch (e) {
      _state = BrokerConnectionState.authFailed;
      _stateController.add(_state);
      _failPendingRequests(Exception('Authentication failed: $e'));
    }
  }

  void _failPendingRequests(Exception error) {
    for (final completer in _pendingRequests.values) {
      if (!completer.isCompleted) {
        completer.completeError(error);
      }
    }
    _pendingRequests.clear();
  }

  Future<ApplicationResponseEnvelope> sendRequest({
    required String method,
    required String path,
    Map<String, String>? headers,
    String? body,
    Duration timeout = const Duration(seconds: 8),
  }) async {
    if (_state != BrokerConnectionState.authenticatedBrokerSessionActive || _channel == null || _e2eSession == null) {
      throw StateError('Broker WebSocket is not connected or authenticated');
    }

    _sequenceNum++;
    final requestId = 'req_${_sequenceNum}_${DateTime.now().millisecondsSinceEpoch}';
    final timestamp = DateTime.now().microsecondsSinceEpoch;

    final appReq = {
      'v': '1.2',
      'session_id': sessionId,
      'request_id': requestId,
      'method': method,
      'path': path,
      if (headers != null) 'headers': headers,
      'body': body ?? '',
      'seq_num': _sequenceNum,
      'timestamp': timestamp,
    };

    final plainJson = jsonEncode(appReq);

    final cipherHex = await _e2eSession!.encrypt(
      direction: Direction.clientToHost,
      plainText: plainJson,
      seqNum: _sequenceNum,
      timestamp: timestamp,
    );

    final frameEnvelope = {
      'v': '1.2',
      'session_id': sessionId,
      'type': 'data',
      'encrypted_payload': cipherHex,
      'seq_num': _sequenceNum,
      'timestamp': timestamp,
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
    _channel = null;
    _state = BrokerConnectionState.disconnected;
    _stateController.add(_state);
    _failPendingRequests(Exception('Client disconnected'));
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
