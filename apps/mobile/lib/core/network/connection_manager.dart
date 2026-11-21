import 'dart:async';
import 'package:flutter/foundation.dart';

enum ConnectionMode { lan, internet }

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
  reconnecting,
  failed,
  revoked,
}

class ConnectionManager extends ChangeNotifier {
  ConnectionStatus _status = ConnectionStatus.disconnected;
  ConnectionMode _mode = ConnectionMode.lan;
  String _activeHostAddress = '192.168.1.100:8082';
  String? _sessionToken;
  Timer? _heartbeatTimer;

  ConnectionStatus get status => _status;
  ConnectionMode get mode => _mode;
  String get activeHostAddress => _activeHostAddress;
  String? get sessionToken => _sessionToken;

  void setConnectionMode(ConnectionMode newMode) {
    _mode = newMode;
    notifyListeners();
  }

  Future<bool> connectToHost(String hostAddress, String token) async {
    _status = ConnectionStatus.connecting;
    _activeHostAddress = hostAddress;
    _sessionToken = token;
    notifyListeners();

    await Future.delayed(const Duration(milliseconds: 300));
    _status = ConnectionStatus.connected;
    _startHeartbeat();
    notifyListeners();
    return true;
  }

  void _startHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      if (_status == ConnectionStatus.connected) {
        if (kDebugMode) {
          print('Ping: DevOrbit Host Bridge Heartbeat OK');
        }
      }
    });
  }

  void disconnect() {
    _heartbeatTimer?.cancel();
    _status = ConnectionStatus.disconnected;
    _sessionToken = null;
    notifyListeners();
  }

  void handleRevocation() {
    _heartbeatTimer?.cancel();
    _status = ConnectionStatus.revoked;
    _sessionToken = null;
    notifyListeners();
  }

  @override
  void dispose() {
    _heartbeatTimer?.cancel();
    super.dispose();
  }
}
