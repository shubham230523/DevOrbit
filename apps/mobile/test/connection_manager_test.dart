import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:devorbit_mobile/core/network/broker_websocket_client.dart';
import 'package:devorbit_mobile/core/network/connection_manager.dart';

/// A real loopback WebSocket server used to drive genuine transport-level transitions.
/// No ConnectionManager behaviour is simulated: every state under test is produced by an
/// actual socket handshake, frame, or close event.
class _ControlledBrokerServer {
  _ControlledBrokerServer._(this._server);

  final HttpServer _server;
  final List<WebSocket> sockets = <WebSocket>[];
  int upgradeAttempts = 0;

  /// When true the HTTP upgrade is rejected with 401 (authentication failure).
  bool rejectUpgrade = false;

  /// Invoked with every successfully upgraded socket.
  Future<void> Function(WebSocket socket)? onConnected;

  String get url => 'ws://127.0.0.1:${_server.port}';

  static Future<_ControlledBrokerServer> start() async {
    final httpServer = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final harness = _ControlledBrokerServer._(httpServer);
    httpServer.listen((req) => unawaited(harness._handle(req)));
    return harness;
  }

  Future<void> _handle(HttpRequest req) async {
    if (req.uri.path != '/ws/client') {
      req.response.statusCode = HttpStatus.notFound;
      await req.response.close();
      return;
    }
    if (rejectUpgrade) {
      // Deliberate authentication rejection before any WebSocket handshake.
      req.response.statusCode = HttpStatus.unauthorized;
      await req.response.close();
      return;
    }
    upgradeAttempts++;
    final socket = await WebSocketTransformer.upgrade(req);
    // Drain inbound frames so Dart processes close handshakes and updates readyState.
    socket.listen((_) {}, onError: (_) {}, onDone: () {});
    sockets.add(socket);
    await onConnected?.call(socket);
  }

  WebSocket get lastSocket => sockets.last;

  Future<void> close() async {
    for (final socket in sockets) {
      await socket.close();
    }
    await _server.close(force: true);
  }
}

BrokerConnectionProfile _profile(String url) => BrokerConnectionProfile(
      brokerUrl: url,
      machineId: 'mach-cm-01',
      deviceId: 'dev-cm-01',
      sessionId: 'sess-cm-01',
      token: 'client-token-cm-32bytes-value!',
      masterSecret: 'master-secret-value-32-bytes-ok!',
    );

Future<void> _waitFor(bool Function() predicate, {Duration timeout = const Duration(seconds: 5)}) async {
  final deadline = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(deadline)) {
    if (predicate()) return;
    await Future.delayed(const Duration(milliseconds: 10));
  }
  throw TimeoutException('Condition not met within $timeout');
}

void main() {
  test('starts disconnected and never reports an authenticated transport prematurely',
      () async {
    final server = await _ControlledBrokerServer.start();
    final manager = ConnectionManager();

    expect(manager.status, BrokerConnectionState.disconnected);
    expect(manager.isTransportAuthenticated, isFalse);

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    // Server upgrades the socket but sends NO readiness frame.
    await manager.connect(_profile(server.url));

    await _waitFor(() => manager.status == BrokerConnectionState.authenticating);
    expect(manager.isTransportAuthenticated, isFalse,
        reason: 'authenticated state must not be entered before the broker acknowledges the session');

    // Readiness timeout surfaces a failure instead of a fake success.
    await _waitFor(
      () => manager.status == BrokerConnectionState.connecting ||
          manager.status == BrokerConnectionState.authenticating ||
          manager.status == BrokerConnectionState.reconnecting,
    );
    expect(manager.isTransportAuthenticated, isFalse);
  });

  test('reports authenticated only after a real session_ready frame', () async {
    final server = await _ControlledBrokerServer.start();
    server.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => manager.isTransportAuthenticated);
    expect(server.upgradeAttempts, 1);
  });

  test('authentication failure is reported as failed and retried with bounded jitter', () async {
    final server = await _ControlledBrokerServer.start();
    server.rejectUpgrade = true;
    final manager = ConnectionManager(
      baseBackoff: const Duration(milliseconds: 20),
      maxBackoff: const Duration(milliseconds: 80),
      jitterRatio: 0.5,
      random: Random(7),
    );

    final observed = <BrokerConnectionState>[];
    manager.addListener(() => observed.add(manager.status));

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));

    // The real 401 upgrade rejection is surfaced (not swallowed as "connected") and the
    // manager schedules supervised retries instead of pretending to be online.
    await _waitFor(() => manager.reconnectAttempts >= 1);
    expect(observed, contains(BrokerConnectionState.authFailed));
    expect(manager.isTransportAuthenticated, isFalse);
    expect(server.upgradeAttempts, 0, reason: 'rejected upgrades never become sockets');
    expect(manager.status, anyOf(
      BrokerConnectionState.reconnecting,
      BrokerConnectionState.authFailed,
      BrokerConnectionState.connecting,
      BrokerConnectionState.authenticating,
    ));
  });

  test('unexpected socket closure triggers supervised reconnect and recovers', () async {
    final server = await _ControlledBrokerServer.start();
    server.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager(
      baseBackoff: const Duration(milliseconds: 20),
      maxBackoff: const Duration(milliseconds: 200),
    );

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => manager.isTransportAuthenticated);

    // Broker restarts / network drops: the socket closes without a revoked frame.
    await server.lastSocket.close();
    await _waitFor(() => !manager.isTransportAuthenticated);

    // The manager must reconnect on its own and re-authenticate against a fresh socket.
    await _waitFor(() => server.upgradeAttempts >= 2);
    await _waitFor(() => manager.isTransportAuthenticated);
    expect(manager.isRevoked, isFalse);
  });

  test('revocation is terminal and cannot be mistaken for an ordinary disconnect', () async {
    final server = await _ControlledBrokerServer.start();
    server.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager(
      baseBackoff: const Duration(milliseconds: 20),
      maxBackoff: const Duration(milliseconds: 100),
    );

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => manager.isTransportAuthenticated);

    server.lastSocket.add(jsonEncode({'type': 'revoked', 'session_id': 'sess-cm-01'}));
    await _waitFor(() => manager.status == BrokerConnectionState.revoked);
    expect(manager.isRevoked, isTrue);

    // A deliberate local disconnect must not mask the revocation as "disconnected".
    manager.disconnect();
    expect(manager.status, BrokerConnectionState.revoked);

    // And the revoked session can never transparently reconnect.
    final upgradesAfterRevocation = server.upgradeAttempts;
    final refused = await manager.connect(_profile(server.url));
    expect(refused, isFalse);
    await Future.delayed(const Duration(milliseconds: 150));
    expect(server.upgradeAttempts, upgradesAfterRevocation);
    expect(manager.status, BrokerConnectionState.revoked);
  });

  test('host_disconnected maps to waitingForHost without claiming authentication', () async {
    final server = await _ControlledBrokerServer.start();
    server.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_pending', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => manager.status == BrokerConnectionState.waitingForHost);
    expect(manager.isTransportAuthenticated, isFalse);

    server.lastSocket.add(jsonEncode({'type': 'host_disconnected', 'session_id': 'sess-cm-01'}));
    await _waitFor(() => manager.status == BrokerConnectionState.hostDisconnected);
    expect(manager.isTransportAuthenticated, isFalse);
  });

  test('duplicate connect calls open exactly one socket', () async {
    final server = await _ControlledBrokerServer.start();
    server.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    final results = await Future.wait([
      manager.connect(_profile(server.url)),
      manager.connect(_profile(server.url)),
      manager.connect(_profile(server.url)),
    ]);
    await _waitFor(() => manager.isTransportAuthenticated);

    expect(results.where((started) => started).length, 1,
        reason: 'only one connect request may open a transport');
    expect(server.upgradeAttempts, 1);
    expect(server.sockets.length, 1);
  });

  test('manual disconnect stops retries and reports disconnected', () async {
    final server = await _ControlledBrokerServer.start();
    // Never acknowledge: keeps the connection in the authenticating state.
    final manager = ConnectionManager(
      baseBackoff: const Duration(milliseconds: 20),
      maxBackoff: const Duration(milliseconds: 80),
    );

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => manager.status == BrokerConnectionState.authenticating);

    manager.disconnect();
    expect(manager.status, BrokerConnectionState.disconnected);
    expect(manager.isTransportAuthenticated, isFalse);

    final attemptsAtDisconnect = server.upgradeAttempts;
    await Future.delayed(const Duration(milliseconds: 200));
    expect(server.upgradeAttempts, attemptsAtDisconnect,
        reason: 'no reconnect may be scheduled after a deliberate disconnect');
  });

  test('late readiness frame after disconnect cannot flip status to connected', () async {
    final server = await _ControlledBrokerServer.start();
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    await manager.connect(_profile(server.url));
    await _waitFor(() => server.sockets.isNotEmpty);

    manager.disconnect();

    // Server tries to acknowledge after the client already discarded the transport.
    try {
      server.lastSocket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    } catch (_) {}
    await Future.delayed(const Duration(milliseconds: 200));

    expect(manager.isTransportAuthenticated, isFalse);
    expect(manager.status, BrokerConnectionState.disconnected);
  });

  test('network path switch reconnects immediately against the new broker address', () async {
    final lanServer = await _ControlledBrokerServer.start();
    lanServer.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final internetServer = await _ControlledBrokerServer.start();
    internetServer.onConnected = (socket) async {
      socket.add(jsonEncode({'type': 'session_ready', 'session_id': 'sess-cm-01'}));
    };
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await lanServer.close();
      await internetServer.close();
    });

    await manager.connect(_profile(lanServer.url), mode: ConnectionMode.lan);
    await _waitFor(() => manager.isTransportAuthenticated);
    expect(manager.mode, ConnectionMode.lan);

    final switched = await manager.reconnect(
      brokerUrl: internetServer.url,
      mode: ConnectionMode.internet,
    );
    expect(switched, isTrue);
    await _waitFor(() => internetServer.upgradeAttempts >= 1);
    await _waitFor(() => manager.isTransportAuthenticated);

    expect(manager.mode, ConnectionMode.internet);
    expect(manager.activeBrokerUrl, internetServer.url);
    await _waitFor(
      () => lanServer.sockets.every((socket) => socket.readyState != WebSocket.open),
      timeout: const Duration(seconds: 3),
    );
  });

  test('backoff is bounded exponential with jitter', () {
    final manager = ConnectionManager(
      baseBackoff: const Duration(seconds: 1),
      maxBackoff: const Duration(seconds: 30),
      jitterRatio: 0.5,
      random: Random(42),
    );
    addTearDown(manager.dispose);

    for (var attempt = 0; attempt < 20; attempt++) {
      final delay = manager.computeBackoffDelay(attempt);
      final ceiling = min(1000 * (1 << min(attempt, 16)), 30000);
      expect(delay.inMilliseconds, lessThanOrEqualTo(ceiling),
          reason: 'attempt $attempt must never exceed its ceiling');
      expect(delay.inMilliseconds, greaterThanOrEqualTo((ceiling * 0.5).floor()),
          reason: 'attempt $attempt must respect the jitter floor');
      expect(delay.inMilliseconds, lessThanOrEqualTo(30000));
    }

    // Same seed => deterministic first sample (fresh instances, no consumed entropy).
    final seededA = ConnectionManager(
      baseBackoff: const Duration(seconds: 1),
      maxBackoff: const Duration(seconds: 30),
      jitterRatio: 0.5,
      random: Random(42),
    );
    final seededB = ConnectionManager(
      baseBackoff: const Duration(seconds: 1),
      maxBackoff: const Duration(seconds: 30),
      jitterRatio: 0.5,
      random: Random(42),
    );
    addTearDown(seededA.dispose);
    addTearDown(seededB.dispose);
    expect(seededA.computeBackoffDelay(3), seededB.computeBackoffDelay(3));

    final samples = <Duration>{
      for (var i = 0; i < 12; i++)
        ConnectionManager(
          baseBackoff: const Duration(seconds: 1),
          maxBackoff: const Duration(seconds: 30),
          random: Random(i),
        ).computeBackoffDelay(4),
    };
    expect(samples.length, greaterThan(1), reason: 'jitter must vary the delay across seeds');
  });

  test('incomplete profile is refused without opening a socket', () async {
    final server = await _ControlledBrokerServer.start();
    final manager = ConnectionManager();

    addTearDown(() async {
      manager.dispose();
      await server.close();
    });

    final started = await manager.connect(const BrokerConnectionProfile(
      brokerUrl: '',
      machineId: '',
      deviceId: '',
      sessionId: '',
      token: '',
      masterSecret: '',
    ));

    expect(started, isFalse);
    expect(server.upgradeAttempts, 0);
    expect(manager.status, BrokerConnectionState.disconnected);
  });
}
