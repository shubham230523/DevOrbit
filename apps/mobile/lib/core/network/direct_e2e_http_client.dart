import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:crypto/crypto.dart' as crypto;
import 'package:http/http.dart' as http;
import '../security/e2e_session.dart';

/// Shared E2E HTTP transport for direct Host Bridge LAN/HTTPS communication.
/// Encrypts every request and response with HKDF-derived AES-256-GCM keys
/// on the `http` channel, authenticates outer requests with HKDF-derived
/// HMAC-SHA256 signatures (never sending raw bearer tokens), and enforces
/// monotonic per-session sequence numbers.
class DirectE2EHttpTransport {
  static final Map<String, int> _sessionSequences = {};
  static final Map<String, Future<void>> _sessionQueues = {};

  final String baseUrl;
  final String? sessionToken;
  final String? sessionId;
  final String? deviceId;
  final String? masterSecret;
  final http.Client _httpClient;

  FlutterE2ESession? _e2eSession;
  List<int>? _httpAuthKey;

  DirectE2EHttpTransport({
    required this.baseUrl,
    this.sessionToken,
    this.sessionId,
    this.deviceId,
    this.masterSecret,
    http.Client? httpClient,
  }) : _httpClient = httpClient ?? http.Client();

  bool get hasE2ECredentials =>
      sessionId != null &&
      sessionId!.isNotEmpty &&
      deviceId != null &&
      deviceId!.isNotEmpty &&
      masterSecret != null &&
      masterSecret!.isNotEmpty;

  Future<FlutterE2ESession> _getSession() async {
    _e2eSession ??= await FlutterE2ESession.create(
      sessionId: sessionId!,
      deviceId: deviceId!,
      masterSecret: masterSecret!,
      channel: 'http',
    );
    return _e2eSession!;
  }

  Future<List<int>> _getHttpAuthKey() async {
    _httpAuthKey ??= await FlutterE2ESession.deriveHttpAuthKey(
      sessionId: sessionId!,
      deviceId: deviceId!,
      masterSecret: masterSecret!,
    );
    return _httpAuthKey!;
  }

  static String _generateHexNonce([int numBytes = 16]) {
    final random = Random.secure();
    final bytes = List<int>.generate(numBytes, (_) => random.nextInt(256));
    return bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  }

  static int _nextSequence(String sessId) {
    final current = _sessionSequences[sessId];
    final next = (current == null)
        ? DateTime.now().millisecondsSinceEpoch
        : current + 1;
    final safeNext = next <= 0 ? 1 : next;
    _sessionSequences[sessId] = safeNext;
    return safeNext;
  }

  /// Sends an authenticated, E2E AES-256-GCM encrypted HTTP request via POST /api/v1/e2e.
  Future<http.Response> send({
    required String method,
    required String path,
    Map<String, String>? headers,
    String? body,
    Duration timeout = const Duration(seconds: 10),
  }) async {
    if (!hasE2ECredentials) {
      throw StateError('Authenticated E2E session credentials are required');
    }

    final sessId = sessionId!;
    final previous = _sessionQueues[sessId];
    final lock = Completer<void>();
    _sessionQueues[sessId] = lock.future;

    try {
      if (previous != null) {
        await previous;
      }

      final e2e = await _getSession();
      final seqNum = _nextSequence(sessId);
      final timestamp = DateTime.now().microsecondsSinceEpoch;
      final requestId = 'http_e2e_${seqNum}_$timestamp';

      final appReq = {
        'v': '1.2',
        'session_id': sessId,
        'request_id': requestId,
        'method': method,
        'path': path,
        if (headers != null && headers.isNotEmpty) 'headers': headers,
        'body': body ?? '',
        'seq_num': seqNum,
        'timestamp': timestamp,
      };

      final cipherHex = await e2e.encrypt(
        direction: Direction.clientToHost,
        plainText: jsonEncode(appReq),
        seqNum: seqNum,
        timestamp: timestamp,
      );

      final outerFrame = jsonEncode({
        'v': '1.2',
        'session_id': sessId,
        'type': 'data',
        'encrypted_payload': cipherHex,
        'seq_num': seqNum,
        'timestamp': timestamp,
      });

      final cleanBase = baseUrl.endsWith('/')
          ? baseUrl.substring(0, baseUrl.length - 1)
          : baseUrl;
      final endpointUri = Uri.parse('$cleanBase/api/v1/e2e');
      final requestUri = endpointUri.hasQuery
          ? '${endpointUri.path}?${endpointUri.query}'
          : endpointUri.path;

      final authKey = await _getHttpAuthKey();
      final authTs = DateTime.now().toUtc().microsecondsSinceEpoch.toString();
      final authNonce = _generateHexNonce();
      final bodyHashHex = crypto.sha256.convert(utf8.encode(outerFrame)).toString();
      final signature = FlutterE2ESession.computeHttpRequestMac(
        authKey: authKey,
        method: 'POST',
        requestUri: requestUri,
        sessionId: sessId,
        deviceId: deviceId!,
        timestamp: authTs,
        nonce: authNonce,
        bodySha256Hex: bodyHashHex,
      );

      final res = await _httpClient
          .post(
            endpointUri,
            headers: {
              'Content-Type': 'application/json',
              'X-DevOrbit-Session-ID': sessId,
              'X-DevOrbit-Device-ID': deviceId!,
              'X-DevOrbit-Timestamp': authTs,
              'X-DevOrbit-Nonce': authNonce,
              'X-DevOrbit-Signature': signature,
            },
            body: outerFrame,
          )
          .timeout(timeout);

      if (res.statusCode != 200) {
        return res;
      }

      final respFrame = jsonDecode(res.body) as Map<String, dynamic>;
      final respSessionId = respFrame['session_id']?.toString() ?? '';
      if (respSessionId != sessId) {
        throw StateError('E2E response session_id mismatch');
      }

      final respCipher = respFrame['encrypted_payload']?.toString() ?? '';
      final respSeq = respFrame['seq_num'] as int? ?? seqNum;
      final respTs = respFrame['timestamp'] as int? ?? 0;

      final plainRespStr = await e2e.decrypt(
        direction: Direction.hostToClient,
        cipherTextHex: respCipher,
        seqNum: respSeq,
        timestamp: respTs,
      );

      final appResp = jsonDecode(plainRespStr) as Map<String, dynamic>;
      final appStatus = appResp['status_code'] as int? ?? 500;
      final appBody = appResp['body']?.toString() ?? '';
      final appHeaders = (appResp['headers'] as Map<String, dynamic>?)?.map(
            (k, v) => MapEntry(k, v.toString()),
          ) ??
          const {'content-type': 'application/json'};

      return http.Response(appBody, appStatus, headers: appHeaders);
    } finally {
      lock.complete();
    }
  }

  /// Resolves a stored broker WebSocket URL into the corresponding Host Bridge HTTP base URL.
  static String resolveHostBridgeHttpUrl(String? brokerUrl) {
    if (brokerUrl == null || brokerUrl.trim().isEmpty) {
      return 'http://localhost:8082';
    }
    final parsed = Uri.tryParse(brokerUrl.trim());
    if (parsed == null || parsed.host.isEmpty) {
      return 'http://localhost:8082';
    }
    final scheme = parsed.scheme.toLowerCase() == 'wss' ? 'https' : 'http';
    final port = (parsed.port == 8081 || !parsed.hasPort) ? 8082 : parsed.port;
    return parsed.replace(scheme: scheme, port: port, path: '').toString();
  }
}
