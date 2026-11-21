import 'dart:convert';

class QrPairingPayload {
  final String version;
  final String challengeId;
  final String hostId;
  final String hostName;
  final String pairingPayload;
  final String pairingSecret;
  final String confirmUrl;
  final String? brokerUrl;
  final DateTime expiresAt;

  QrPairingPayload({
    required this.version,
    required this.challengeId,
    required this.hostId,
    required this.hostName,
    required this.pairingPayload,
    required this.pairingSecret,
    required this.confirmUrl,
    this.brokerUrl,
    required this.expiresAt,
  });

  bool get isExpired => DateTime.now().isAfter(expiresAt);

  factory QrPairingPayload.fromJson(Map<String, dynamic> json) {
    final v = json['v']?.toString();
    if (v != '1.0' && v != '1') {
      throw FormatException('Unsupported QR pairing protocol version: $v');
    }

    final challengeId = json['challenge_id']?.toString() ?? json['id']?.toString();
    final hostId = json['host_id']?.toString() ?? json['machine_id']?.toString();
    final hostName = json['host_name']?.toString() ?? 'Desktop Host';
    final pairingPayload = json['pairing_payload']?.toString() ?? json['payload']?.toString();
    final pairingSecret = json['pairing_secret']?.toString() ?? '';
    final confirmUrl = json['confirm_url']?.toString() ?? json['control_plane_url']?.toString();
    final brokerUrl = json['broker_url']?.toString();
    final expiresAtStr = json['expires_at']?.toString();

    if (challengeId == null || challengeId.isEmpty) {
      throw const FormatException('QR pairing payload missing challenge_id');
    }
    if (hostId == null || hostId.isEmpty) {
      throw const FormatException('QR pairing payload missing host_id');
    }
    if (pairingPayload == null || pairingPayload.isEmpty) {
      throw const FormatException('QR pairing payload missing pairing_payload');
    }
    if (confirmUrl == null || confirmUrl.isEmpty) {
      throw const FormatException('QR pairing payload missing confirm_url / control_plane_url');
    }
    if (expiresAtStr == null || expiresAtStr.isEmpty) {
      throw const FormatException('QR pairing payload missing expires_at');
    }

    final expiresAt = DateTime.parse(expiresAtStr);

    return QrPairingPayload(
      version: v!,
      challengeId: challengeId,
      hostId: hostId,
      hostName: hostName,
      pairingPayload: pairingPayload,
      pairingSecret: pairingSecret,
      confirmUrl: confirmUrl,
      brokerUrl: brokerUrl,
      expiresAt: expiresAt,
    );
  }

  factory QrPairingPayload.fromRawString(String raw) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, dynamic>) {
        throw const FormatException('QR code content is not a JSON object');
      }
      return QrPairingPayload.fromJson(decoded);
    } on FormatException {
      rethrow;
    } catch (e) {
      throw FormatException('Failed to parse QR pairing payload: $e');
    }
  }

  Map<String, dynamic> toJson() {
    return {
      'v': version,
      'challenge_id': challengeId,
      'host_id': hostId,
      'host_name': hostName,
      'pairing_payload': pairingPayload,
      if (pairingSecret.isNotEmpty) 'pairing_secret': pairingSecret,
      'confirm_url': confirmUrl,
      if (brokerUrl != null && brokerUrl!.isNotEmpty) 'broker_url': brokerUrl,
      'expires_at': expiresAt.toUtc().toIso8601String(),
    };
  }
}
