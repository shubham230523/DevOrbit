import 'dart:async';
import 'dart:math';

import 'package:flutter/foundation.dart';

import 'broker_websocket_client.dart';

/// Network path currently in use for the broker connection.
enum ConnectionMode { lan, internet }

/// Immutable description of the authenticated session used to reach the broker.
class BrokerConnectionProfile {
  final String brokerUrl;
  final String machineId;
  final String deviceId;
  final String sessionId;
  final String token;
  final String masterSecret;

  const BrokerConnectionProfile({
    required this.brokerUrl,
    required this.machineId,
    required this.deviceId,
    required this.sessionId,
    required this.token,
    required this.masterSecret,
  });

  bool get isComplete =>
      brokerUrl.trim().isNotEmpty &&
      machineId.isNotEmpty &&
      deviceId.isNotEmpty &&
      sessionId.isNotEmpty &&
      token.isNotEmpty &&
      masterSecret.isNotEmpty;

  BrokerConnectionProfile withBrokerUrl(String newBrokerUrl) => BrokerConnectionProfile(
        brokerUrl: newBrokerUrl,
        machineId: machineId,
        deviceId: deviceId,
        sessionId: sessionId,
        token: token,
        masterSecret: masterSecret,
      );

  @override
  String toString() =>
      'BrokerConnectionProfile(machine: $machineId, session: $sessionId, url: $brokerUrl)';
}

/// Creates the transport used to reach the broker. Injected so tests can supply a
/// client wired to a controlled WebSocket server, and so production code has exactly
/// one place where a real socket is created.
typedef BrokerConnectionFactory = BrokerWebSocketClient Function(BrokerConnectionProfile profile);

BrokerWebSocketClient _defaultBrokerConnectionFactory(BrokerConnectionProfile profile) {
  return BrokerWebSocketClient(
    brokerUrl: profile.brokerUrl,
    machineId: profile.machineId,
    deviceId: profile.deviceId,
    sessionId: profile.sessionId,
    token: profile.token,
    masterSecret: profile.masterSecret,
    // Reconnect/backoff policy is owned by this manager, not by the transport, so that
    // exactly one component schedules attempts (no duplicated or racing reconnect loops).
    autoReconnect: false,
  );
}

/// Owns the broker connection lifecycle and derives every observable state from the real
/// authenticated transport.
///
/// Guarantees:
/// * [isTransportAuthenticated] is only true after the broker has explicitly acknowledged the
///   session (`session_ready`/`auth_ack`); nothing here can fake connectivity.
/// * Revocation and expiry are terminal and can never be rendered as a plain disconnect.
/// * Repeated [connect] calls cannot open duplicate sockets.
/// * Asynchronous events belonging to a superseded socket are discarded (generation guard).
/// * Retries use bounded exponential backoff with jitter.
class ConnectionManager extends ChangeNotifier {
  ConnectionManager({
    BrokerConnectionFactory? connectionFactory,
    Duration baseBackoff = const Duration(seconds: 1),
    Duration maxBackoff = const Duration(seconds: 30),
    double jitterRatio = 0.5,
    Random? random,
  })  : _connectionFactory = connectionFactory ?? _defaultBrokerConnectionFactory,
        _baseBackoff = baseBackoff,
        _maxBackoff = maxBackoff,
        _jitterRatio = jitterRatio,
        _random = random ?? Random();

  final BrokerConnectionFactory _connectionFactory;
  final Duration _baseBackoff;
  final Duration _maxBackoff;
  final double _jitterRatio;
  final Random _random;

  BrokerConnectionState _status = BrokerConnectionState.disconnected;
  ConnectionMode _mode = ConnectionMode.lan;
  BrokerConnectionProfile? _profile;
  BrokerWebSocketClient? _client;
  StreamSubscription<BrokerConnectionState>? _subscription;
  Timer? _reconnectTimer;

  /// Monotonic epoch. Incremented whenever the active transport is replaced or discarded, so
  /// late events from an older socket can never mutate current state.
  int _generation = 0;
  int _reconnectAttempts = 0;
  bool _opening = false;
  bool _manualDisconnect = true;
  bool _revoked = false;
  bool _expired = false;
  bool _disposed = false;

  ConnectionMode get mode => _mode;
  BrokerConnectionState get status => _status;
  BrokerWebSocketClient? get client => _client;
  String get activeBrokerUrl => _profile?.brokerUrl ?? '';
  int get reconnectAttempts => _reconnectAttempts;
  bool get isRevoked => _revoked;
  bool get isExpired => _expired;

  /// True only when the broker has authenticated this session over an established transport.
  bool get isTransportAuthenticated =>
      _status == BrokerConnectionState.authenticatedBrokerSessionActive;

  /// True while a connection attempt or retry is pending.
  bool get isBusy =>
      _status == BrokerConnectionState.connecting ||
      _status == BrokerConnectionState.transportConnected ||
      _status == BrokerConnectionState.authenticating ||
      _status == BrokerConnectionState.reconnecting;

  /// Bounded exponential backoff with jitter for the given 0-based [attempt].
  Duration computeBackoffDelay(int attempt) {
    final cappedAttempt = attempt < 0 ? 0 : (attempt > 16 ? 16 : attempt);
    var millis = _baseBackoff.inMilliseconds * (1 << cappedAttempt);
    final maxMillis = _maxBackoff.inMilliseconds;
    if (millis > maxMillis) {
      millis = maxMillis;
    }
    final ratio = _jitterRatio.clamp(0.0, 1.0);
    if (ratio > 0 && millis > 0) {
      final spread = (millis * ratio).round();
      if (spread > 0) {
        millis -= _random.nextInt(spread + 1);
      }
    }
    return Duration(milliseconds: millis < 0 ? 0 : millis);
  }

  /// Starts a supervised connection.
  ///
  /// Returns false when the request was refused: the session is revoked/expired (credentials
  /// must be re-paired), or an attempt is already in progress/active (duplicate prevention).
  Future<bool> connect(BrokerConnectionProfile profile, {ConnectionMode? mode}) async {
    if (_disposed) return false;
    if (_revoked || _expired) return false;
    if (!profile.isComplete) return false;
    if (_opening || _subscription != null || isTransportAuthenticated) return false;

    _profile = profile;
    if (mode != null) _mode = mode;
    _manualDisconnect = false;
    _reconnectAttempts = 0;
    await _openTransport();
    return true;
  }

  Future<void> _openTransport() async {
    final profile = _profile;
    if (profile == null || _manualDisconnect || _revoked || _expired || _disposed) return;
    if (_opening) return;

    _opening = true;
    final generation = ++_generation;
    final previous = _client;
    _client = null;
    _subscription = null;
    if (previous != null) {
      previous.disconnect();
      previous.dispose();
    }

    _setStatus(BrokerConnectionState.connecting);

    try {
      final client = _connectionFactory(profile);
      if (generation != _generation) {
        client.dispose();
        return;
      }
      _client = client;
      _subscription = client.stateStream.listen(
        (state) => _onTransportState(generation, state),
        onError: (_) => _onTransportFailure(generation),
        onDone: () => _onTransportFailure(generation),
      );
      await client.connect();
    } catch (_) {
      _onTransportFailure(generation);
    } finally {
      if (generation == _generation) _opening = false;
    }
  }

  void _onTransportState(int generation, BrokerConnectionState state) {
    if (generation != _generation || _disposed) return;

    switch (state) {
      case BrokerConnectionState.authenticatedBrokerSessionActive:
        _reconnectAttempts = 0;
        _cancelReconnectTimer();
        _setStatus(state);
        return;
      case BrokerConnectionState.revoked:
        _revoked = true;
        _cancelReconnectTimer();
        _setStatus(state);
        return;
      case BrokerConnectionState.sessionExpired:
        _expired = true;
        _cancelReconnectTimer();
        _setStatus(state);
        return;
      case BrokerConnectionState.disconnected:
        // A transport-level disconnect that we did not request is a failure to recover from.
        if (_manualDisconnect) {
          _setStatus(state);
        } else {
          _onTransportFailure(generation);
        }
        return;
      case BrokerConnectionState.authFailed:
      case BrokerConnectionState.connectionFailed:
        _setStatus(state);
        _scheduleReconnect();
        return;
      default:
        _setStatus(state);
        return;
    }
  }

  void _onTransportFailure(int generation) {
    if (generation != _generation || _disposed) return;
    if (_revoked || _expired || _manualDisconnect) return;
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed || _manualDisconnect || _revoked || _expired) return;
    if (_reconnectTimer != null) return;

    final attempt = _reconnectAttempts;
    _reconnectAttempts = attempt + 1;
    _setStatus(BrokerConnectionState.reconnecting);

    final delay = computeBackoffDelay(attempt);
    final generation = _generation;
    _reconnectTimer = Timer(delay, () {
      _reconnectTimer = null;
      if (generation != _generation || _disposed || _manualDisconnect || _revoked || _expired) {
        return;
      }
      unawaited(_openTransport());
    });
  }

  void _cancelReconnectTimer() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
  }

  /// Forces an immediate reconnect attempt, e.g. after a Wi-Fi to mobile-network transition
  /// where the broker address changed.
  Future<bool> reconnect({String? brokerUrl, ConnectionMode? mode}) async {
    if (_disposed) return false;
    if (_revoked || _expired) return false;
    final profile = _profile;
    if (profile == null) return false;

    _cancelReconnectTimer();
    _manualDisconnect = false;
    _reconnectAttempts = 0;
    if (brokerUrl != null && brokerUrl.trim().isNotEmpty) {
      _profile = profile.withBrokerUrl(brokerUrl.trim());
    }
    if (mode != null) _mode = mode;

    // Discard the current transport before opening a new one; the generation bump inside
    // _openTransport guarantees any late event from the old socket is ignored.
    final previous = _client;
    _subscription?.cancel();
    _subscription = null;
    _client = null;
    if (previous != null) {
      previous.disconnect();
      previous.dispose();
    }
    await _openTransport();
    return true;
  }

  /// Marks the session revoked locally (e.g. server-issued revocation observed elsewhere) and
  /// closes the transport. Revocation is terminal: [connect] will refuse until [resetAfterRePairing].
  void handleRevocation() {
    if (_disposed) return;
    _revoked = true;
    _cancelReconnectTimer();
    _teardownTransport();
    _setStatus(BrokerConnectionState.revoked);
  }

  /// Deliberate user disconnect. Never reported as a failure and never triggers a retry.
  void disconnect() {
    if (_disposed) return;
    _manualDisconnect = true;
    _cancelReconnectTimer();
    _teardownTransport();
    if (!_revoked && !_expired) {
      _setStatus(BrokerConnectionState.disconnected);
    }
  }

  /// Clears a terminal revoked/expired state after fresh credentials have been paired.
  void resetAfterRePairing() {
    if (_disposed) return;
    _revoked = false;
    _expired = false;
    _reconnectAttempts = 0;
    _setStatus(BrokerConnectionState.disconnected);
  }

  void _teardownTransport() {
    _generation++;
    _subscription?.cancel();
    _subscription = null;
    final client = _client;
    _client = null;
    if (client != null) {
      client.disconnect();
      client.dispose();
    }
    _opening = false;
  }

  void _setStatus(BrokerConnectionState next) {
    if (_status == next || _disposed) return;
    _status = next;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _manualDisconnect = true;
    _cancelReconnectTimer();
    _teardownTransport();
    super.dispose();
  }
}
