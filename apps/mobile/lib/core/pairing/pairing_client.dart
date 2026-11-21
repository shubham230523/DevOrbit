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

    final proofSig = secureStorage.generateDeviceProof(
      payload.pairingPayload,
      deviceId,
      payload.pairingSecret,
    );

    final confirmUri = Uri.parse(
      payload.confirmUrl.endsWith('/api/v1/pairing/confirm')
          ? payload.confirmUrl
          : '${payload.confirmUrl.replaceAll(RegExp(r'/+$'), '')}/api/v1/pairing/confirm',
    );

    final reqBody = jsonEncode({
      'challenge_id': payload.challengeId,
      'device_id': deviceId,
      'device_name': deviceName,
      'device_public_key': devicePublicKey ?? '',
      'proof_signature': proofSig,
    });

    http.Response response;
    try {
      response = await _httpClient
          .post(
            confirmUri,
            headers: {
              'Content-Type': 'application/json',
              'Accept': 'application/json',
            },
            body: reqBody,
          )
          .timeout(const Duration(seconds: 10));
    } catch (e) {
      throw PairingException('Failed to connect to host/control plane: $e');
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
    final sessionId = respJson['session_id']?.toString() ?? 'sess-${DateTime.now().millisecondsSinceEpoch}';
    final masterSecret = respJson['session_secret']?.toString() ?? payload.pairingSecret;
    final brokerUrl = respJson['broker_url']?.toString() ?? payload.brokerUrl;
    final expiresAtStr = respJson['expires_at']?.toString();

    if (token == null || token.isEmpty) {
      throw PairingException('Server confirmed pairing but failed to issue a valid session token');
    }
    if (masterSecret.isEmpty) {
      throw PairingException('Server confirmed pairing but failed to provide an E2E session secret');
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
