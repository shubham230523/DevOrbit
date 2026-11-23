import 'dart:convert';
import 'package:http/http.dart' as http;
import '../security/secure_storage.dart';
import 'qr_pairing_payload.dart';

class PairingException implements Exception {
  final String message;
  final int? statusCode;

  PairingException(this.message, [this.statusCode]);

  @override
  String toString() => 'PairingException: $message ${statusCode != null ? "(status: $statusCode)" : ""}';
}

class PairingConfirmationResult {
  final String machineId;
  final String deviceId;
  final String sessionId;
  final String token;
  final String masterSecret;
  final String? brokerUrl;
  final DateTime? expiresAt;

  PairingConfirmationResult({
    required this.machineId,
    required this.deviceId,
    required this.sessionId,
    required this.token,
    required this.masterSecret,
    this.brokerUrl,
    this.expiresAt,
  });
}

class PairingClient {
  final http.Client _httpClient;

  PairingClient({http.Client? httpClient}) : _httpClient = httpClient ?? http.Client();

  Future<PairingConfirmationResult> confirmPairing({
    required QrPairingPayload payload,
    required String deviceId,
    required String deviceName,
    required SecureStorageService secureStorage,
    String? devicePublicKey,
  }) async {
    if (payload.isExpired) {
      throw PairingException('Pairing challenge has expired. Generate a new QR code on the host.');
    }

    final proofSig = secureStorage.generateBoundDeviceProof(
      version: payload.version,
      challengeId: payload.challengeId,
      payload: payload.pairingPayload,
      hostId: payload.hostId,
      deviceId: deviceId,
      expiresAtStr: payload.rawExpiresAt,
      secretKey: payload.pairingSecret,
    );

    // Validate confirmation URI: must be absolute with valid scheme and path
    final confirmUri = Uri.tryParse(payload.confirmUrl);
    if (confirmUri == null || !confirmUri.hasScheme || !confirmUri.hasAuthority || confirmUri.host.isEmpty) {
      throw PairingException('Invalid confirmation URL in QR payload: ${payload.confirmUrl}');
    }
    final scheme = confirmUri.scheme.toLowerCase();
    if (scheme != 'http' && scheme != 'https') {
      throw PairingException('Unsupported URI scheme in confirm_url: $scheme. Must be http or https.');
    }
    if (confirmUri.hasPort && (confirmUri.port <= 0 || confirmUri.port > 65535)) {
      throw PairingException('Invalid port in confirm_url: ${confirmUri.port}');
    }
    if (!confirmUri.path.endsWith('/api/v1/pairing/confirm')) {
      throw PairingException('confirm_url path must end with /api/v1/pairing/confirm, got: ${confirmUri.path}');
    }
    if (confirmUri.userInfo.isNotEmpty) {
      throw PairingException('confirm_url must not contain embedded user credentials');
    }
    if (confirmUri.hasQuery) {
      throw PairingException('confirm_url must not contain query parameters');
    }
    if (confirmUri.hasFragment) {
      throw PairingException('confirm_url must not contain URL fragments');
    }

    final reqBody = jsonEncode({
      'challenge_id': payload.challengeId,
      'device_id': deviceId,
      'device_name': deviceName,
      'device_public_key': devicePublicKey ?? '',
      'proof_signature': proofSig,
    });

    final port = confirmUri.hasPort ? confirmUri.port : 8082;
    final candidateUris = <Uri>[
      Uri.parse('http://localhost:$port/api/v1/pairing/confirm'),
      confirmUri,
      Uri.parse('http://10.0.2.2:$port/api/v1/pairing/confirm'),
    ];

    http.Response? response;
    Object? lastError;

    for (final uri in candidateUris) {
      try {
        final res = await _httpClient
            .post(
              uri,
              headers: {
                'Content-Type': 'application/json',
                'Accept': 'application/json',
              },
              body: reqBody,
            )
            .timeout(const Duration(seconds: 3));

        response = res;
        if (res.statusCode == 200 || res.statusCode == 201) {
          break;
        }
      } catch (e) {
        lastError = e;
      }
    }

    if (response == null) {
      throw PairingException('Failed to connect to host/control plane (${payload.confirmUrl}): $lastError');
    }

    if (response.statusCode != 200 && response.statusCode != 201) {
      String errMsg = 'Pairing verification failed with status ${response.statusCode}';
      try {
        final errJson = jsonDecode(response.body);
        if (errJson is Map<String, dynamic> && errJson.containsKey('error')) {
          final serverErr = errJson['error'].toString();
          if (serverErr.contains('expired')) {
            errMsg = 'Pairing challenge has expired. Please generate a new QR code.';
          } else if (serverErr.contains('consumed') || serverErr.contains('used') || serverErr.contains('already')) {
            errMsg = 'Pairing challenge has already been consumed. Please generate a new QR code.';
          } else if (serverErr.contains('proof')) {
            errMsg = 'Cryptographic proof of possession rejected by host.';
          } else {
            errMsg = serverErr;
          }
        }
      } catch (_) {}
      throw PairingException(errMsg, response.statusCode);
    }

    final Map<String, dynamic> respJson = jsonDecode(response.body) as Map<String, dynamic>;

    final token = respJson['session_token']?.toString();
    final sessionId = respJson['session_id']?.toString();
    final masterSecret = respJson['session_secret']?.toString();
    final respMachineId = respJson['machine_id']?.toString();
    final respDeviceId = respJson['device_id']?.toString();
    final brokerUrl = respJson['broker_url']?.toString() ?? payload.brokerUrl;
    final expiresAtStr = respJson['expires_at']?.toString();

    // Mandatory server-issued credentials validation
    if (token == null || token.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but failed to issue a valid session token (session_token)');
    }
    if (sessionId == null || sessionId.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but failed to issue a valid session ID (session_id)');
    }
    if (masterSecret == null || masterSecret.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but failed to provide an E2E session secret (session_secret)');
    }
    if (masterSecret == payload.pairingSecret) {
      throw PairingException('Server insecurely echoed QR pairing secret as E2E session secret');
    }
    if (respMachineId == null || respMachineId.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but omitted required machine_id');
    }
    if (respMachineId != payload.hostId) {
      throw PairingException('Server machine_id ($respMachineId) mismatch with QR host_id (${payload.hostId})');
    }
    if (respDeviceId == null || respDeviceId.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but omitted required device_id');
    }
    if (respDeviceId != deviceId) {
      throw PairingException('Server device_id ($respDeviceId) mismatch with requested device_id ($deviceId)');
    }
    if (brokerUrl == null || brokerUrl.trim().isEmpty) {
      throw PairingException('Server confirmed pairing but omitted broker_url');
    }
    final bUri = Uri.tryParse(brokerUrl);
    if (bUri == null || !bUri.hasScheme || !bUri.hasAuthority || bUri.host.isEmpty) {
      throw PairingException('Server returned invalid broker_url: $brokerUrl');
    }
    final bScheme = bUri.scheme.toLowerCase();
    if (bScheme != 'ws' && bScheme != 'wss') {
      throw PairingException('Server returned non-WebSocket broker_url scheme ($bScheme). Must be ws or wss.');
    }
    if (bUri.userInfo.isNotEmpty) {
      throw PairingException('Server returned broker_url containing embedded user credentials');
    }

    // Persist securely to platform storage
    await secureStorage.savePairedSession(
      machineId: payload.hostId,
      deviceId: deviceId,
      sessionId: sessionId,
      token: token,
      masterSecret: masterSecret,
      brokerUrl: brokerUrl,
    );

    return PairingConfirmationResult(
      machineId: payload.hostId,
      deviceId: deviceId,
      sessionId: sessionId,
      token: token,
      masterSecret: masterSecret,
      brokerUrl: brokerUrl,
      expiresAt: expiresAtStr != null ? DateTime.tryParse(expiresAtStr) : null,
    );
  }
}
