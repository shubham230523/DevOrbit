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
  waitingForHost,
  authenticatedBrokerSessionActive,
  reconnecting,
  sessionExpired,
  authFailed,
  connectionFailed,
  revoked,
  hostDisconnected,
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
  final Duration readinessTimeout;

  WebSocketChannel? _channel;
  StreamSubscription? _channelSubscription;
  FlutterE2ESession? _e2eSession;
  BrokerConnectionState _state = BrokerConnectionState.disconnected;
  final StreamController<BrokerConnectionState> _stateController = StreamController<BrokerConnectionState>.broadcast();
  final Map<String, Completer<ApplicationResponseEnvelope>> _pendingRequests = {};
  int _sequenceNum = 0;

  bool _isReconnecting = false;
  bool _manuallyDisconnected = false;
  Timer? _reconnectTimer;
  Timer? _readinessTimer;
  int _reconnectAttempts = 0;
  static const int _maxBackoffSeconds = 30;

  BrokerConnectionState get state => _state;
  Stream<BrokerConnectionState> get stateStream => _stateController.stream;

  BrokerWebSocketClient({
    required this.brokerUrl,
    required this.machineId,
    required this.deviceId,
    required this.sessionId,
    required this.token,
    required this.masterSecret,
    this.readinessTimeout = const Duration(seconds: 30),
  });

  void _updateState(BrokerConnectionState newState) {
    if (_state != newState && !_stateController.isClosed) {
      _state = newState;
      _stateController.add(_state);
    }
  }

  Future<void> connect() async {
    _manuallyDisconnected = false;
    _updateState(BrokerConnectionState.connecting);

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

      await _channelSubscription?.cancel();
      _channel?.sink.close();

      _channel = IOWebSocketChannel.connect(
        wsUri,
        headers: {
          'Authorization': 'Bearer $token',
        },
      );
      await _channel!.ready;

      _updateState(BrokerConnectionState.transportConnected);
      _updateState(BrokerConnectionState.authenticating);

      // Start readiness timer: if no session_ready or session_pending arrives in time, fail
      _readinessTimer?.cancel();
      _readinessTimer = Timer(readinessTimeout, () {
        if (_state == BrokerConnectionState.authenticating) {
          _updateState(BrokerConnectionState.connectionFailed);
          _failPendingRequests(TimeoutException('Readiness handshake timed out waiting for broker response'));
          _channel?.sink.close();
          _scheduleReconnect();
        }
      });

      _channelSubscription = _channel!.stream.listen(
        (data) async {
          try {
            final jsonMap = jsonDecode(data.toString()) as Map<String, dynamic>;
            final frameType = jsonMap['type'];
            final frameSessionId = jsonMap['session_id'];

            // 1. Host disconnected while client remained connected
            if (frameType == 'host_disconnected') {
              _updateState(BrokerConnectionState.hostDisconnected);
              return;
            }

            // 2. Session Pending (client connected, waiting for host)
            if (frameType == 'session_pending') {
              _readinessTimer?.cancel();
              _updateState(BrokerConnectionState.waitingForHost);
              return;
            }

            // 3. Server-confirmed session readiness
            if (frameType == 'session_ready' || frameType == 'auth_ack') {
              // Validate session identity
              if (frameSessionId != null && frameSessionId != sessionId) {
                // Session mismatch; do not treat as ready
                return;
              }
              _readinessTimer?.cancel();
              _reconnectAttempts = 0;
              _isReconnecting = false;
              _updateState(BrokerConnectionState.authenticatedBrokerSessionActive);
              return;
            }

            // 4. Server-side session revocation
            if (frameType == 'revoked') {
              _readinessTimer?.cancel();
              _cancelReconnect();
              _updateState(BrokerConnectionState.revoked);
              _failPendingRequests(Exception('Session revoked by server'));
              _channel?.sink.close();
              _channel = null;
              return;
            }

            // 5. Session expired
            if (frameType == 'expired' || (frameType == 'error' && jsonMap['error']?.toString().contains('expired') == true)) {
              _readinessTimer?.cancel();
              _cancelReconnect();
              _updateState(BrokerConnectionState.sessionExpired);
              _failPendingRequests(Exception('Session expired'));
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
          _readinessTimer?.cancel();
          if (_state != BrokerConnectionState.revoked && _state != BrokerConnectionState.sessionExpired) {
            _failPendingRequests(Exception('WebSocket error: $error'));
            _scheduleReconnect();
          }
        },
        onDone: () {
          _readinessTimer?.cancel();
          if (_state != BrokerConnectionState.revoked && _state != BrokerConnectionState.sessionExpired) {
            _failPendingRequests(Exception('WebSocket connection closed'));
            if (!_manuallyDisconnected) {
              _scheduleReconnect();
            } else {
              _updateState(BrokerConnectionState.disconnected);
            }
          }
        },
      );
    } catch (e) {
      _readinessTimer?.cancel();
      _updateState(BrokerConnectionState.authFailed);
      _failPendingRequests(Exception('Authentication failed: $e'));
      if (!_manuallyDisconnected) {
        _scheduleReconnect();
      }
    }
  }

  void _scheduleReconnect() {
    if (_manuallyDisconnected ||
        _state == BrokerConnectionState.revoked ||
        _state == BrokerConnectionState.sessionExpired) {
      return;
    }
    if (_isReconnecting) return;
    _isReconnecting = true;

    _updateState(BrokerConnectionState.reconnecting);

    int delaySeconds = 1 << _reconnectAttempts;
    if (delaySeconds > _maxBackoffSeconds) {
      delaySeconds = _maxBackoffSeconds;
    }
    _reconnectAttempts++;

    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(Duration(seconds: delaySeconds), () async {
      _isReconnecting = false;
      if (_manuallyDisconnected ||
          _state == BrokerConnectionState.revoked ||
          _state == BrokerConnectionState.sessionExpired) {
        return;
      }
      try {
        await connect();
      } catch (_) {}
    });
  }

  void _cancelReconnect() {
    _isReconnecting = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _reconnectAttempts = 0;
  }

  void _failPendingRequests(Exception error) {
    for (final completer in _pendingRequests.values) {
      if (!completer.isCompleted) {
        completer.completeError(error);
      }
    }
    _pendingRequests.clear();
  }

  Future<void>? _sendQueue;

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

    final completer = Completer<ApplicationResponseEnvelope>();
    late String requestId;

    final previousTask = _sendQueue;
    final taskCompleter = Completer<void>();
    _sendQueue = taskCompleter.future;

    try {
      if (previousTask != null) {
        await previousTask;
      }

      if (_state != BrokerConnectionState.authenticatedBrokerSessionActive || _channel == null || _e2eSession == null) {
        throw StateError('Broker WebSocket is not connected or authenticated');
      }

      _sequenceNum++;
      final currentSeq = _sequenceNum;
      requestId = 'req_${currentSeq}_${DateTime.now().millisecondsSinceEpoch}';
      final timestamp = DateTime.now().microsecondsSinceEpoch;

      final appReq = {
        'v': '1.2',
        'session_id': sessionId,
        'request_id': requestId,
        'method': method,
        'path': path,
        if (headers != null) 'headers': headers,
        'body': body ?? '',
        'seq_num': currentSeq,
        'timestamp': timestamp,
      };

      final plainJson = jsonEncode(appReq);

      final cipherHex = await _e2eSession!.encrypt(
        direction: Direction.clientToHost,
        plainText: plainJson,
        seqNum: currentSeq,
        timestamp: timestamp,
      );

      final frameEnvelope = {
        'v': '1.2',
        'session_id': sessionId,
        'type': 'data',
        'encrypted_payload': cipherHex,
        'seq_num': currentSeq,
        'timestamp': timestamp,
      };

      _pendingRequests[requestId] = completer;
      _channel!.sink.add(jsonEncode(frameEnvelope));
    } catch (e) {
      if (!completer.isCompleted) {
        completer.completeError(e);
      }
    } finally {
      taskCompleter.complete();
    }

    return completer.future.timeout(
      timeout,
      onTimeout: () {
        _pendingRequests.remove(requestId);
        throw TimeoutException('Request $requestId timed out waiting for response frame');
      },
    );
  }

  void disconnect() {
    _manuallyDisconnected = true;
    _cancelReconnect();
    _readinessTimer?.cancel();
    _channelSubscription?.cancel();
    _channel?.sink.close();
    _channel = null;
    _updateState(BrokerConnectionState.disconnected);
    _failPendingRequests(Exception('Client disconnected'));
  }

  void dispose() {
    _manuallyDisconnected = true;
    _cancelReconnect();
    _readinessTimer?.cancel();
    _channelSubscription?.cancel();
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
